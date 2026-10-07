package store

import (
	"context"
	"fmt"
)

// SyncablePRReviews returns the ids of PR reviews still open or submitted —
// the set whose GitHub pollers the daemon resumes at boot.
func (s *Store) SyncablePRReviews(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id FROM subjects s JOIN review_meta m ON m.subject_id = s.id
		  WHERE m.kind = ? AND s.status IN ('open','submitted') ORDER BY s.created_at`, ReviewKindPR)
	if err != nil {
		return nil, fmt.Errorf("list syncable pr reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
