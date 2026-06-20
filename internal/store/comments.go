package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const commentCols = `id, version_id, file_path, side, start_line, end_line, start_side, end_side, line_content, body, author, status, created_at, updated_at`

func scanComment(row interface{ Scan(...any) error }) (Comment, error) {
	var (
		c                Comment
		created, updated int64
	)
	if err := row.Scan(&c.ID, &c.VersionID, &c.FilePath, &c.Side, &c.StartLine, &c.EndLine,
		&c.StartSide, &c.EndSide, &c.LineContent, &c.Body, &c.Author, &c.Status, &created, &updated); err != nil {
		return Comment{}, err
	}
	c.CreatedAt = fromUnix(created)
	c.UpdatedAt = fromUnix(updated)
	return c, nil
}

// CreateComment inserts a new comment and returns its id.
func (s *Store) CreateComment(ctx context.Context, c Comment) (int64, error) {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO comments(version_id, file_path, side, start_line, end_line, start_side, end_side, line_content, body, author, status, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.VersionID, c.FilePath, c.Side, c.StartLine, c.EndLine, c.StartSide, c.EndSide,
		c.LineContent, c.Body, defaultStr(c.Author, "user"), defaultStr(c.Status, "open"), unix(now), unix(now))
	if err != nil {
		return 0, fmt.Errorf("create comment: %w", err)
	}
	return res.LastInsertId()
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

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
