package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const eventCols = `review_id, seq, origin, type, version_number, payload, created_at, dedup_key`

func scanEvent(row interface{ Scan(...any) error }) (Event, error) {
	var (
		e       Event
		payload string
		dedup   sql.NullString
		created int64
	)
	if err := row.Scan(&e.ReviewID, &e.Seq, &e.Origin, &e.Type, &e.VersionNumber, &payload, &created, &dedup); err != nil {
		return Event{}, err
	}
	e.Payload = []byte(payload)
	e.DedupKey = dedup.String
	e.CreatedAt = fromUnix(created)
	return e, nil
}

// AppendEvent allocates the next per-review seq and appends the event, returning
// the assigned seq. The MAX(seq)+1 read and the insert run in one transaction on
// the single writer, so seq is gap-free and monotonic. When DedupKey is set and
// already present, the existing event's seq is returned and nothing is inserted.
func (s *Store) AppendEvent(ctx context.Context, e *Event) (int64, error) {
	if e.DedupKey != "" {
		var seq int64
		err := s.db.QueryRowContext(ctx, `SELECT seq FROM events WHERE dedup_key=?`, e.DedupKey).Scan(&seq)
		if err == nil {
			return seq, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("event dedup lookup: %w", err)
		}
	}
	payload := e.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin event tx: %w", err)
	}
	defer tx.Rollback()

	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE review_id=?`, e.ReviewID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("next event seq: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events(review_id, seq, origin, type, version_number, payload, created_at, dedup_key)
		 VALUES(?,?,?,?,?,?,?,?)`,
		e.ReviewID, seq, e.Origin, e.Type, e.VersionNumber, string(payload), unix(time.Now()), nullString(e.DedupKey)); err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit event: %w", err)
	}
	e.Seq = seq
	return seq, nil
}

// EventsSince returns events with seq greater than cursor, oldest first.
// excludeClaude drops origin='claude' rows (the long-poll/channel filter that
// kills the echo loop); the browser passes false to see every origin.
func (s *Store) EventsSince(ctx context.Context, reviewID string, cursor int64, excludeClaude bool) ([]Event, error) {
	q := `SELECT ` + eventCols + ` FROM events WHERE review_id=? AND seq>?`
	if excludeClaude {
		q += ` AND origin<>'claude'`
	}
	q += ` ORDER BY seq ASC`
	rows, err := s.db.QueryContext(ctx, q, reviewID, cursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
