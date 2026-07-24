package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const upsertFileState = `
INSERT INTO file_states(review_id, section_key, path, reviewed, hidden, reviewed_fingerprint, updated_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(review_id, section_key, path) DO UPDATE SET
  reviewed=excluded.reviewed, hidden=excluded.hidden,
  reviewed_fingerprint=excluded.reviewed_fingerprint, updated_at=excluded.updated_at`

// ApplyFileStates upserts a batch of partial state changes in one transaction,
// returning each file's prior and applied state in input order. fingerprints
// maps each (section, path) to its current diff fingerprint, stamped when a file
// turns reviewed and cleared when it turns unreviewed.
func (s *Store) ApplyFileStates(ctx context.Context, reviewID string, inputs []FileStateInput, fingerprints map[SectionFileKey]string) ([]FileStateResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin file-states tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := unix(time.Now())
	results := make([]FileStateResult, 0, len(inputs))
	for _, in := range inputs {
		var prior PriorState
		err := tx.QueryRowContext(ctx,
			`SELECT reviewed, hidden, reviewed_fingerprint FROM file_states WHERE review_id=? AND section_key=? AND path=?`,
			reviewID, in.SectionKey, in.Path).Scan(&prior.Reviewed, &prior.Hidden, &prior.Fingerprint)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read file state %s: %w", in.Path, err)
		}
		applied := AppliedState{Reviewed: prior.Reviewed, Hidden: prior.Hidden}
		if in.Reviewed != nil {
			applied.Reviewed = *in.Reviewed
		}
		if in.Hidden != nil {
			applied.Hidden = *in.Hidden
		}
		// A file already reviewed keeps its stamp (the unmark rule compares
		// against the fingerprint current when it was marked); a fresh mark
		// stamps the current fingerprint; an unmark clears it.
		fp := ""
		if applied.Reviewed {
			fp = prior.Fingerprint
			if !prior.Reviewed {
				fp = fingerprints[SectionFileKey{SectionKey: in.SectionKey, Path: in.Path}]
			}
		}
		if _, err := tx.ExecContext(ctx, upsertFileState,
			reviewID, in.SectionKey, in.Path, boolInt(applied.Reviewed), boolInt(applied.Hidden), fp, now); err != nil {
			return nil, fmt.Errorf("apply file state %s: %w", in.Path, err)
		}
		results = append(results, FileStateResult{SectionKey: in.SectionKey, Path: in.Path, Prior: prior, Applied: applied})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit file states: %w", err)
	}
	return results, nil
}

// RestoreFileStates writes each change's prior state back, for undo. It is
// last-write-wins over human changes made after the AI batch — accepted,
// documented semantics.
func (s *Store) RestoreFileStates(ctx context.Context, reviewID string, changes []AIChange) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restore tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := unix(time.Now())
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, upsertFileState,
			reviewID, c.SectionKey, c.Path, boolInt(c.Prior.Reviewed), boolInt(c.Prior.Hidden), c.Prior.Fingerprint, now); err != nil {
			return fmt.Errorf("restore file state %s: %w", c.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore: %w", err)
	}
	return nil
}

// ListFileStates returns every file-state row of a review, ordered by
// (section, path).
func (s *Store) ListFileStates(ctx context.Context, reviewID string) ([]FileState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT review_id, section_key, path, reviewed, hidden, reviewed_fingerprint, updated_at
		   FROM file_states WHERE review_id=? ORDER BY section_key ASC, path ASC`, reviewID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FileState
	for rows.Next() {
		var (
			st               FileState
			reviewed, hidden int
			updated          int64
		)
		if err := rows.Scan(&st.ReviewID, &st.SectionKey, &st.Path, &reviewed, &hidden, &st.ReviewedFingerprint, &updated); err != nil {
			return nil, err
		}
		st.Reviewed = reviewed != 0
		st.Hidden = hidden != 0
		st.UpdatedAt = fromUnix(updated)
		out = append(out, st)
	}
	return out, rows.Err()
}

// UnreviewChangedFiles unmarks every reviewed file whose current fingerprint
// no longer matches the one stamped at review time, returning the unmarked
// rows (post-update). Files absent from fingerprints (disappeared from their
// section) are untouched; hidden flags are preserved.
func (s *Store) UnreviewChangedFiles(ctx context.Context, reviewID string, fingerprints map[SectionFileKey]string) ([]FileState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin unreview tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		`SELECT section_key, path, hidden, reviewed_fingerprint FROM file_states WHERE review_id=? AND reviewed=1 ORDER BY section_key ASC, path ASC`,
		reviewID)
	if err != nil {
		return nil, err
	}
	var unmarked []FileState
	for rows.Next() {
		var (
			sectionKey, path, stamped string
			hidden                    int
		)
		if err := rows.Scan(&sectionKey, &path, &hidden, &stamped); err != nil {
			_ = rows.Close()
			return nil, err
		}
		current, ok := fingerprints[SectionFileKey{SectionKey: sectionKey, Path: path}]
		if !ok || current == stamped {
			continue
		}
		unmarked = append(unmarked, FileState{ReviewID: reviewID, SectionKey: sectionKey, Path: path, Hidden: hidden != 0})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now()
	for i := range unmarked {
		if _, err := tx.ExecContext(ctx,
			`UPDATE file_states SET reviewed=0, reviewed_fingerprint='', updated_at=? WHERE review_id=? AND section_key=? AND path=?`,
			unix(now), reviewID, unmarked[i].SectionKey, unmarked[i].Path); err != nil {
			return nil, fmt.Errorf("unreview %s: %w", unmarked[i].Path, err)
		}
		unmarked[i].UpdatedAt = now
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit unreview: %w", err)
	}
	return unmarked, nil
}
