package store

import (
	"context"
	"fmt"
)

// MaxEventSeq returns the review's highest event seq, 0 when it has none. The
// session response carries it so the SPA can tell replayed SSE frames (seq at
// or below it) from live ones.
func (s *Store) MaxEventSeq(ctx context.Context, reviewID string) (int64, error) {
	var seq int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0) FROM events WHERE subject_id=?`, reviewID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("max event seq: %w", err)
	}
	return seq, nil
}

// LastSubmittedVersion returns the highest version number the review has a
// submit event for, or 0 when it has never been submitted — the boundary
// feedback uses to bound stranded-thread collection to the current round.
func (s *Store) LastSubmittedVersion(ctx context.Context, reviewID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(CAST(json_extract(payload,'$.version_number') AS INTEGER)), 0)
		   FROM events WHERE subject_id=? AND type=?`, reviewID, EventSubmit).Scan(&n); err != nil {
		return 0, fmt.Errorf("last submitted version: %w", err)
	}
	return n, nil
}

// StaleConnectedReviews returns the ids of reviews whose most recent
// channel.changed event reports connected:true — the set the daemon's boot
// reconcile closes out after a daemon death lost the SSE detach.
func (s *Store) StaleConnectedReviews(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT subject_id FROM events
		WHERE type=? AND json_extract(payload,'$.connected')=1
		  AND seq=(SELECT MAX(seq) FROM events e2 WHERE e2.subject_id=events.subject_id AND e2.type=?)`,
		EventChannelChanged, EventChannelChanged)
	if err != nil {
		return nil, fmt.Errorf("stale connected reviews: %w", err)
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
