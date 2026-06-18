package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const versionCols = `id, review_id, version_number, branch, base_ref, patch_path, files_json, session_id, created_at`

func scanVersion(row interface{ Scan(...any) error }) (Version, error) {
	var (
		v       Version
		created int64
	)
	if err := row.Scan(&v.ID, &v.ReviewID, &v.VersionNumber, &v.Branch, &v.BaseRef, &v.PatchPath, &v.FilesJSON, &v.SessionID, &created); err != nil {
		return Version{}, err
	}
	v.CreatedAt = fromUnix(created)
	return v, nil
}

// CreateVersion appends the next-numbered version to a review. The number is
// allocated as MAX(version_number)+1 on the single writer, so it is gap-free and
// race-free.
func (s *Store) CreateVersion(ctx context.Context, reviewID, branch, baseRef, patchPath, filesJSON, sessionID string) (Version, error) {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, fmt.Errorf("begin version tx: %w", err)
	}
	defer tx.Rollback()

	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_number),0)+1 FROM review_versions WHERE review_id=?`, reviewID).Scan(&next); err != nil {
		return Version{}, fmt.Errorf("next version number: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO review_versions(review_id, version_number, branch, base_ref, patch_path, files_json, session_id, created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		reviewID, next, branch, baseRef, patchPath, filesJSON, sessionID, unix(now))
	if err != nil {
		return Version{}, fmt.Errorf("insert version: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return Version{}, fmt.Errorf("commit version: %w", err)
	}
	return Version{
		ID: id, ReviewID: reviewID, VersionNumber: next, Branch: branch, BaseRef: baseRef,
		PatchPath: patchPath, FilesJSON: filesJSON, SessionID: sessionID, CreatedAt: now,
	}, nil
}

// UpdateVersionPatchPath sets a version's on-disk patch path after the snapshot
// has been written (the path embeds the version number allocated at insert).
func (s *Store) UpdateVersionPatchPath(ctx context.Context, id int64, patchPath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE review_versions SET patch_path=? WHERE id=?`, patchPath, id)
	if err != nil {
		return fmt.Errorf("update version patch path: %w", err)
	}
	return nil
}

// LatestVersion returns the highest-numbered version of a review. ok is false
// when the review has no versions yet.
func (s *Store) LatestVersion(ctx context.Context, reviewID string) (Version, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+versionCols+` FROM review_versions WHERE review_id=? ORDER BY version_number DESC LIMIT 1`, reviewID)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, false, nil
	}
	return v, err == nil, err
}

// GetVersion returns a specific version of a review, or ErrNotFound.
func (s *Store) GetVersion(ctx context.Context, reviewID string, versionNumber int) (Version, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+versionCols+` FROM review_versions WHERE review_id=? AND version_number=?`, reviewID, versionNumber)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	return v, err
}

// GetVersionByID returns a version by its surrogate id, or ErrNotFound.
func (s *Store) GetVersionByID(ctx context.Context, id int64) (Version, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+versionCols+` FROM review_versions WHERE id=?`, id)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	return v, err
}

// ListVersions returns all versions of a review, oldest first.
func (s *Store) ListVersions(ctx context.Context, reviewID string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+versionCols+` FROM review_versions WHERE review_id=? ORDER BY version_number ASC`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
