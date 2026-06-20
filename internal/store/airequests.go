package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidTransition reports an AI-request status change outside the allowed
// graph.
var ErrInvalidTransition = errors.New("invalid ai request transition")

// aiTransitions maps a target status to the statuses it may come from.
// working→working is allowed so a redelivered ai.request.created or a retried
// update_ai_request re-asserts idempotently instead of being refused.
// awaiting_input parks a request on a clarifying question; answered (REST-only,
// from the human) makes it re-dispatchable, so working/done/failed accept it.
var aiTransitions = map[string][]string{
	"working":        {"pending", "working", "answered"},
	"awaiting_input": {"pending", "working"},
	"answered":       {"awaiting_input"},
	"done":           {"pending", "working", "answered"},
	"failed":         {"pending", "working", "answered", "awaiting_input"},
	"undone":         {"done"},
}

const aiRequestCols = `id, review_id, version_number, source, prompt, status, summary, unmatched_json, changes_json, created_at, updated_at, question_json, answer_json, attempt, phase`

func scanAIRequest(row interface{ Scan(...any) error }) (AIRequest, error) {
	var (
		r                                                    AIRequest
		unmatchedJSON, changesJSON, questionJSON, answerJSON string
		created, updated                                     int64
	)
	if err := row.Scan(&r.ID, &r.ReviewID, &r.VersionNumber, &r.Source, &r.Prompt, &r.Status,
		&r.Summary, &unmatchedJSON, &changesJSON, &created, &updated, &questionJSON, &answerJSON, &r.Attempt, &r.Phase); err != nil {
		return AIRequest{}, err
	}
	if err := json.Unmarshal([]byte(unmatchedJSON), &r.Unmatched); err != nil {
		return AIRequest{}, fmt.Errorf("ai request %d: decode unmatched: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(changesJSON), &r.Changes); err != nil {
		return AIRequest{}, fmt.Errorf("ai request %d: decode changes: %w", r.ID, err)
	}
	if questionJSON != "" {
		var q AIQuestion
		if err := json.Unmarshal([]byte(questionJSON), &q); err != nil {
			return AIRequest{}, fmt.Errorf("ai request %d: decode question: %w", r.ID, err)
		}
		r.Question = &q
	}
	if answerJSON != "" {
		var a AIAnswer
		if err := json.Unmarshal([]byte(answerJSON), &a); err != nil {
			return AIRequest{}, fmt.Errorf("ai request %d: decode answer: %w", r.ID, err)
		}
		r.Answer = &a
	}
	r.CreatedAt = fromUnix(created)
	r.UpdatedAt = fromUnix(updated)
	return r, nil
}

// CreateAIRequest inserts a new pending request and returns it.
func (s *Store) CreateAIRequest(ctx context.Context, reviewID string, versionNumber int, source, prompt string) (AIRequest, error) {
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_requests(review_id, version_number, source, prompt, status, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?)`,
		reviewID, versionNumber, source, prompt, "pending", unix(now), unix(now))
	if err != nil {
		return AIRequest{}, fmt.Errorf("create ai request: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AIRequest{}, err
	}
	return AIRequest{
		ID: id, ReviewID: reviewID, VersionNumber: versionNumber, Source: source, Prompt: prompt,
		Status: "pending", Unmatched: []Unmatched{}, Changes: []AIChange{}, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// GetAIRequest returns a request by id, or ErrNotFound.
func (s *Store) GetAIRequest(ctx context.Context, id int64) (AIRequest, error) {
	r, err := scanAIRequest(s.db.QueryRowContext(ctx,
		`SELECT `+aiRequestCols+` FROM ai_requests WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AIRequest{}, fmt.Errorf("ai request %d: %w", id, ErrNotFound)
	}
	return r, err
}

// ListAIRequests returns every request on a review, newest first.
func (s *Store) ListAIRequests(ctx context.Context, reviewID string) ([]AIRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+aiRequestCols+` FROM ai_requests WHERE review_id=? ORDER BY created_at DESC, id DESC`, reviewID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AIRequest
	for rows.Next() {
		r, err := scanAIRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListOpenAIRequests returns a version's open (pending or working) requests,
// newest first — the set /cc-review:start re-offers so a freshly attached
// session dispatches any request (system organize or a human's AI-bar prompt)
// left open while no live session was watching. Scoped to one version so a
// prior version's stale organize never leaks onto a newer start.
func (s *Store) ListOpenAIRequests(ctx context.Context, reviewID string, versionNumber int) ([]AIRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+aiRequestCols+` FROM ai_requests WHERE review_id=? AND version_number=? AND status IN ('pending','working','answered') ORDER BY created_at DESC, id DESC`,
		reviewID, versionNumber)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AIRequest
	for rows.Next() {
		r, err := scanAIRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AwaitingInputRequests returns a review's user requests parked on a clarifying
// question (awaiting_input), newest first — the new-version path fails the ones a
// superseded version stranded, since the version-scoped open set never re-offers
// them and the sweeper only touches pending.
func (s *Store) AwaitingInputRequests(ctx context.Context, reviewID string) ([]AIRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+aiRequestCols+` FROM ai_requests WHERE review_id=? AND status='awaiting_input' ORDER BY created_at DESC, id DESC`,
		reviewID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AIRequest
	for rows.Next() {
		r, err := scanAIRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StalePendingUserRequests returns user-sourced requests still pending since
// before the cutoff — the sweeper's candidates to fail when no live session
// ever dispatched them. System organize requests are closed on resume instead.
func (s *Store) StalePendingUserRequests(ctx context.Context, before time.Time) ([]AIRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+aiRequestCols+` FROM ai_requests WHERE source=? AND status='pending' AND created_at < ? ORDER BY id`,
		OriginUser, unix(before))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AIRequest
	for rows.Next() {
		r, err := scanAIRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// transitionAIRequest applies a guarded status change inside a transaction:
// aiTransitions[to] must admit the current status (ErrInvalidTransition
// otherwise), then updateFn writes the new status plus any side fields, and the
// reread row is returned. Every status-changing method routes through here so the
// transition graph is enforced in exactly one place.
func (s *Store) transitionAIRequest(ctx context.Context, id int64, to string, updateFn func(context.Context, *sql.Tx) error) (AIRequest, error) {
	allowed, ok := aiTransitions[to]
	if !ok {
		return AIRequest{}, fmt.Errorf("ai request %d: unknown status %q", id, to)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AIRequest{}, fmt.Errorf("begin transition tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	err = tx.QueryRowContext(ctx, `SELECT status FROM ai_requests WHERE id=?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return AIRequest{}, fmt.Errorf("ai request %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return AIRequest{}, fmt.Errorf("read ai request %d: %w", id, err)
	}
	from := false
	for _, st := range allowed {
		from = from || st == current
	}
	if !from {
		return AIRequest{}, fmt.Errorf("ai request %d is %q, cannot become %q: %w", id, current, to, ErrInvalidTransition)
	}
	if err := updateFn(ctx, tx); err != nil {
		return AIRequest{}, err
	}
	updated, err := scanAIRequest(tx.QueryRowContext(ctx, `SELECT `+aiRequestCols+` FROM ai_requests WHERE id=?`, id))
	if err != nil {
		return AIRequest{}, fmt.Errorf("reread ai request %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return AIRequest{}, fmt.Errorf("commit transition: %w", err)
	}
	return updated, nil
}

// MarkWorking moves a request to working and records an optional progress phase
// label (empty keeps the stored one). The working→working self-transition lets a
// live agent stream successive phase labels without clobbering summary or unmatched.
func (s *Store) MarkWorking(ctx context.Context, id int64, phase string) (AIRequest, error) {
	return s.transitionAIRequest(ctx, id, "working", func(ctx context.Context, tx *sql.Tx) error {
		query := `UPDATE ai_requests SET status='working', updated_at=?`
		args := []any{unix(time.Now())}
		if phase != "" {
			query += `, phase=?`
			args = append(args, phase)
		}
		if _, err := tx.ExecContext(ctx, query+` WHERE id=?`, append(args, id)...); err != nil {
			return fmt.Errorf("mark working ai request %d: %w", id, err)
		}
		return nil
	})
}

// TransitionAIRequest moves a request to working, done, or failed (the agent's
// lifecycle). An empty summary keeps the stored one; nil unmatched keeps the list.
func (s *Store) TransitionAIRequest(ctx context.Context, id int64, to, summary string, unmatched []Unmatched) (AIRequest, error) {
	return s.transitionAIRequest(ctx, id, to, func(ctx context.Context, tx *sql.Tx) error {
		query := `UPDATE ai_requests SET status=?, updated_at=?`
		args := []any{to, unix(time.Now())}
		if summary != "" {
			query += `, summary=?`
			args = append(args, summary)
		}
		if unmatched != nil {
			b, err := json.Marshal(unmatched)
			if err != nil {
				return fmt.Errorf("encode unmatched: %w", err)
			}
			query += `, unmatched_json=?`
			args = append(args, string(b))
		}
		if _, err := tx.ExecContext(ctx, query+` WHERE id=?`, append(args, id)...); err != nil {
			return fmt.Errorf("transition ai request %d: %w", id, err)
		}
		return nil
	})
}

// AskAIRequest parks a working request on a clarifying question (→awaiting_input).
func (s *Store) AskAIRequest(ctx context.Context, id int64, q AIQuestion) (AIRequest, error) {
	return s.transitionAIRequest(ctx, id, "awaiting_input", func(ctx context.Context, tx *sql.Tx) error {
		b, err := json.Marshal(q)
		if err != nil {
			return fmt.Errorf("encode question: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE ai_requests SET status='awaiting_input', question_json=?, updated_at=? WHERE id=?`,
			string(b), unix(time.Now()), id); err != nil {
			return fmt.Errorf("ask ai request %d: %w", id, err)
		}
		return nil
	})
}

// AnswerAIRequest records the human's answer and bumps attempt (→answered), so
// redelivery dispatches a fresh agent run with the original prompt plus the Q&A.
func (s *Store) AnswerAIRequest(ctx context.Context, id int64, a AIAnswer) (AIRequest, error) {
	return s.transitionAIRequest(ctx, id, "answered", func(ctx context.Context, tx *sql.Tx) error {
		b, err := json.Marshal(a)
		if err != nil {
			return fmt.Errorf("encode answer: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE ai_requests SET status='answered', answer_json=?, attempt=attempt+1, updated_at=? WHERE id=?`,
			string(b), unix(time.Now()), id); err != nil {
			return fmt.Errorf("answer ai request %d: %w", id, err)
		}
		return nil
	})
}

// AppendAIRequestChanges merges a batch of changes into the request's
// changes_json: a path seen before keeps its FIRST prior (undo restores the
// pre-request state) while applied and reason take the latest values.
func (s *Store) AppendAIRequestChanges(ctx context.Context, id int64, changes []AIChange) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin changes tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var changesJSON string
	err = tx.QueryRowContext(ctx, `SELECT changes_json FROM ai_requests WHERE id=?`, id).Scan(&changesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("ai request %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read ai request %d changes: %w", id, err)
	}
	var existing []AIChange
	if err := json.Unmarshal([]byte(changesJSON), &existing); err != nil {
		return fmt.Errorf("ai request %d: decode changes: %w", id, err)
	}
	byPath := make(map[string]int, len(existing))
	for i, c := range existing {
		byPath[c.Path] = i
	}
	for _, c := range changes {
		if i, ok := byPath[c.Path]; ok {
			existing[i].Applied = c.Applied
			existing[i].Reason = c.Reason
			continue
		}
		byPath[c.Path] = len(existing)
		existing = append(existing, c)
	}
	b, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("encode changes: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE ai_requests SET changes_json=?, updated_at=? WHERE id=?`, string(b), unix(time.Now()), id); err != nil {
		return fmt.Errorf("append ai request %d changes: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit changes: %w", err)
	}
	return nil
}
