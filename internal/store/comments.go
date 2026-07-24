package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const commentCols = `id, version_id, section_id, branch, pending, file_path, side, start_line, end_line, start_side, end_side, line_content, body, author, status, created_at, updated_at`

func scanComment(row interface{ Scan(...any) error }) (Comment, error) {
	var (
		c                Comment
		pending          int
		created, updated int64
	)
	if err := row.Scan(&c.ID, &c.VersionID, &c.SectionID, &c.Branch, &pending, &c.FilePath, &c.Side, &c.StartLine, &c.EndLine,
		&c.StartSide, &c.EndSide, &c.LineContent, &c.Body, &c.Author, &c.Status, &created, &updated); err != nil {
		return Comment{}, err
	}
	c.Pending = pending != 0
	c.CreatedAt = fromUnix(created)
	c.UpdatedAt = fromUnix(updated)
	return c, nil
}

// ErrStaleSection reports a comment insert against a section whose version the
// review has already superseded.
var ErrStaleSection = errors.New("comment section belongs to a superseded version")

// CreateComment inserts a comment and returns its id. In one transaction it
// first rejects a section no longer on the review's latest version
// (ErrStaleSection) — the single-writer connection serializes that against
// CreateVersion, so a version minted mid-insert can't strand the comment.
func (s *Store) CreateComment(ctx context.Context, c Comment) (int64, error) {
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin comment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var latestVersionID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM review_versions
		 WHERE review_id = (SELECT review_id FROM review_versions WHERE id = ?)
		 ORDER BY version_number DESC LIMIT 1`, c.VersionID).Scan(&latestVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("resolve latest version: %w", err)
	}
	if c.VersionID != latestVersionID {
		return 0, ErrStaleSection
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO comments(version_id, section_id, branch, pending, file_path, side, start_line, end_line, start_side, end_side, line_content, body, author, status, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.VersionID, c.SectionID, c.Branch, boolInt(c.Pending), c.FilePath, c.Side, c.StartLine, c.EndLine, c.StartSide, c.EndSide,
		c.LineContent, c.Body, defaultStr(c.Author, "user"), defaultStr(c.Status, "open"), unix(now), unix(now))
	if err != nil {
		return 0, fmt.Errorf("create comment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit comment: %w", err)
	}
	return id, nil
}

// GetComment returns a comment by id, or ErrNotFound.
func (s *Store) GetComment(ctx context.Context, id int64) (Comment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+commentCols+` FROM comments WHERE id=?`, id)
	c, err := scanComment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	return c, err
}

// ListCommentsByVersion returns every comment on a version, oldest first.
func (s *Store) ListCommentsByVersion(ctx context.Context, versionID int64) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+commentCols+` FROM comments WHERE version_id=? ORDER BY created_at ASC, id ASC`, versionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCommentStatus sets a comment's status (open|resolved).
func (s *Store) UpdateCommentStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE comments SET status=?, updated_at=? WHERE id=?`, status, unix(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update comment status: %w", err)
	}
	return nil
}

// UpdateCommentBody edits a comment's body.
func (s *Store) UpdateCommentBody(ctx context.Context, id int64, body string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE comments SET body=?, updated_at=? WHERE id=?`, body, unix(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update comment body: %w", err)
	}
	return nil
}

// ResolveCommentContext returns the review id and version number a comment
// belongs to, for tagging events. Returns ErrNotFound when the comment is absent.
func (s *Store) ResolveCommentContext(ctx context.Context, commentID int64) (reviewID string, versionNumber int, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT v.review_id, v.version_number
		   FROM comments c JOIN review_versions v ON v.id = c.version_id
		  WHERE c.id=?`, commentID)
	err = row.Scan(&reviewID, &versionNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return reviewID, versionNumber, err
}

// StrandedComment is an open comment on a superseded version, carried into
// feedback with the version it was written against.
type StrandedComment struct {
	Comment       Comment
	VersionNumber int
}

func scanStrandedComment(row interface{ Scan(...any) error }) (StrandedComment, error) {
	var (
		sc               StrandedComment
		pending          int
		created, updated int64
	)
	c := &sc.Comment
	if err := row.Scan(&c.ID, &c.VersionID, &c.SectionID, &c.Branch, &pending, &c.FilePath, &c.Side, &c.StartLine, &c.EndLine,
		&c.StartSide, &c.EndSide, &c.LineContent, &c.Body, &c.Author, &c.Status, &created, &updated, &sc.VersionNumber); err != nil {
		return StrandedComment{}, err
	}
	c.Pending = pending != 0
	c.CreatedAt = fromUnix(created)
	c.UpdatedAt = fromUnix(updated)
	return sc, nil
}

// ListStrandedOpenComments returns the review's open comments on versions after
// afterVersion and before beforeVersion — threads a version bump superseded
// before they reached a submit — each tagged with its origin version.
func (s *Store) ListStrandedOpenComments(ctx context.Context, reviewID string, afterVersion, beforeVersion int) ([]StrandedComment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.version_id, c.section_id, c.branch, c.pending, c.file_path, c.side, c.start_line, c.end_line,
		        c.start_side, c.end_side, c.line_content, c.body, c.author, c.status, c.created_at, c.updated_at, v.version_number
		   FROM comments c JOIN review_versions v ON v.id = c.version_id
		  WHERE v.review_id=? AND c.status='open' AND v.version_number > ? AND v.version_number < ?
		  ORDER BY v.version_number ASC, c.created_at ASC, c.id ASC`,
		reviewID, afterVersion, beforeVersion)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StrandedComment
	for rows.Next() {
		sc, err := scanStrandedComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
