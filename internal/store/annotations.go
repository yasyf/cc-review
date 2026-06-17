package store

import (
	"context"
	"fmt"
	"time"
)

const annotationCols = `id, version_id, file_path, side, start_line, end_line, label, ai_request_id, created_at`

func scanAnnotation(row interface{ Scan(...any) error }) (Annotation, error) {
	var (
		a       Annotation
		created int64
	)
	if err := row.Scan(&a.ID, &a.VersionID, &a.FilePath, &a.Side, &a.StartLine, &a.EndLine,
		&a.Label, &a.AIRequestID, &created); err != nil {
		return Annotation{}, err
	}
	a.CreatedAt = fromUnix(created)
	return a, nil
}

// CreateAnnotation inserts a Claude-authored highlight and returns its id.
func (s *Store) CreateAnnotation(ctx context.Context, a Annotation) (int64, error) {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO annotations(version_id, file_path, side, start_line, end_line, label, ai_request_id, created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		a.VersionID, a.FilePath, a.Side, a.StartLine, a.EndLine, a.Label, a.AIRequestID, unix(now))
	if err != nil {
		return 0, fmt.Errorf("create annotation: %w", err)
	}
	return res.LastInsertId()
}

// ListAnnotationsByVersion returns every annotation on a version, oldest first.
func (s *Store) ListAnnotationsByVersion(ctx context.Context, versionID int64) ([]Annotation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+annotationCols+` FROM annotations WHERE version_id=? ORDER BY created_at ASC, id ASC`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Annotation
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAnnotationsByAIRequest removes every annotation an AI request created,
// returning the number deleted so undo can decide whether to re-emit. Highlights
// are pure decoration, so undo clears them outright rather than carrying a status.
func (s *Store) DeleteAnnotationsByAIRequest(ctx context.Context, aiRequestID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM annotations WHERE ai_request_id=?`, aiRequestID)
	if err != nil {
		return 0, fmt.Errorf("delete annotations for ai request %d: %w", aiRequestID, err)
	}
	return res.RowsAffected()
}
