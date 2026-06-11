package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Turn is one Claude prompt→stop window in a repo, bracketed by working-tree
// snapshots. Timestamps are unix milliseconds.
type Turn struct {
	ID               int64
	RepoRoot         string
	Backend          string // git | jj
	SessionID        string
	ClaudePID        int
	PromptExcerpt    string
	TranscriptPath   string
	TranscriptOffset int64
	TreeStart        string
	TreeEnd          string // empty until closed
	Status           string // open | closed | interrupted
	StartedAt        int64
	EndedAt          int64 // 0 while open
}

const turnCols = `id, repo_root, backend, session_id, claude_pid, prompt_excerpt, transcript_path, transcript_offset, tree_start, tree_end, status, started_at, ended_at`

func scanTurn(row interface{ Scan(...any) error }) (Turn, error) {
	var t Turn
	if err := row.Scan(&t.ID, &t.RepoRoot, &t.Backend, &t.SessionID, &t.ClaudePID, &t.PromptExcerpt,
		&t.TranscriptPath, &t.TranscriptOffset, &t.TreeStart, &t.TreeEnd, &t.Status, &t.StartedAt, &t.EndedAt); err != nil {
		return Turn{}, err
	}
	return t, nil
}

// CreateTurn inserts a new open turn, stamping Status and StartedAt, and
// returns it with the allocated id.
func (s *Store) CreateTurn(ctx context.Context, t Turn) (Turn, error) {
	t.Status = "open"
	t.StartedAt = time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO turns(repo_root, backend, session_id, claude_pid, prompt_excerpt, transcript_path, transcript_offset, tree_start, tree_end, status, started_at, ended_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.RepoRoot, t.Backend, t.SessionID, t.ClaudePID, t.PromptExcerpt, t.TranscriptPath,
		t.TranscriptOffset, t.TreeStart, t.TreeEnd, t.Status, t.StartedAt, t.EndedAt)
	if err != nil {
		return Turn{}, fmt.Errorf("create turn: %w", err)
	}
	t.ID, err = res.LastInsertId()
	if err != nil {
		return Turn{}, fmt.Errorf("create turn: %w", err)
	}
	return t, nil
}

// CloseTurn ends a turn with its closing tree snapshot and final status,
// stamping ended_at.
func (s *Store) CloseTurn(ctx context.Context, id int64, treeEnd, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE turns SET tree_end=?, status=?, ended_at=? WHERE id=?`,
		treeEnd, status, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("close turn: %w", err)
	}
	return nil
}

// CloseOpenTurnsForWindow marks every open turn of a Claude window (repo +
// pid) interrupted; tree_end stays empty because no closing snapshot exists.
func (s *Store) CloseOpenTurnsForWindow(ctx context.Context, repoRoot string, claudePID int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE turns SET status='interrupted', ended_at=? WHERE repo_root=? AND claude_pid=? AND status='open'`,
		time.Now().UnixMilli(), repoRoot, claudePID)
	if err != nil {
		return fmt.Errorf("close open turns: %w", err)
	}
	return nil
}

// LatestOpenTurn returns the newest open turn of a Claude window (repo + pid).
// ok is false when none is open.
func (s *Store) LatestOpenTurn(ctx context.Context, repoRoot string, claudePID int) (Turn, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+turnCols+` FROM turns WHERE repo_root=? AND claude_pid=? AND status='open' ORDER BY id DESC LIMIT 1`,
		repoRoot, claudePID)
	t, err := scanTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Turn{}, false, nil
	}
	return t, err == nil, err
}

// ListAttributableTurns returns a repo's turns started at or after sinceMs,
// oldest first, capped at 1000.
func (s *Store) ListAttributableTurns(ctx context.Context, repoRoot string, sinceMs int64) ([]Turn, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+turnCols+` FROM turns WHERE repo_root=? AND started_at>=? ORDER BY id LIMIT 1000`,
		repoRoot, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("list attributable turns: %w", err)
	}
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTurnsByIDs returns the turns with the given ids, ordered by id.
func (s *Store) ListTurnsByIDs(ctx context.Context, ids []int64) ([]Turn, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+turnCols+` FROM turns WHERE id IN (`+placeholders+`) ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list turns by ids: %w", err)
	}
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
