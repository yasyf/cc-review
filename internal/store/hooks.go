package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UpsertSessionHook records (or refreshes) what the SessionStart hook reported
// for a Claude session: its cwd and authoritative transcript path.
func (s *Store) UpsertSessionHook(ctx context.Context, h SessionHook) error {
	started := h.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session_hooks(session_id, cwd, transcript_path, started_at) VALUES(?,?,?,?)
		 ON CONFLICT(session_id) DO UPDATE SET cwd=excluded.cwd, transcript_path=excluded.transcript_path`,
		h.SessionID, h.Cwd, h.TranscriptPath, unix(started))
	if err != nil {
		return fmt.Errorf("upsert session hook: %w", err)
	}
	return nil
}

// GetSessionHook returns the recorded hook for a session. ok is false when none.
func (s *Store) GetSessionHook(ctx context.Context, sessionID string) (SessionHook, bool, error) {
	var (
		h       SessionHook
		started int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT session_id, cwd, transcript_path, started_at FROM session_hooks WHERE session_id=?`, sessionID).
		Scan(&h.SessionID, &h.Cwd, &h.TranscriptPath, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionHook{}, false, nil
	}
	if err != nil {
		return SessionHook{}, false, err
	}
	h.StartedAt = fromUnix(started)
	return h, true, nil
}
