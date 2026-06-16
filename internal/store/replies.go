package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrReviewNotOpen reports an ask answer racing Submit: the owning review froze
// between the card render and the POST.
var ErrReviewNotOpen = errors.New("review is not open")

const replyCols = `id, comment_id, origin, kind, body, ask_json, answered, answer, answered_via, created_at`

// OpenQuestion is a Claude question awaiting an answer, with enough comment
// context to surface it in the feedback drain.
type OpenQuestion struct {
	ReplyID     int64
	CommentID   int64
	FilePath    string
	StartLine   int
	CommentBody string
	Question    string
	Ask         *Ask // kind=ask only
}

func scanReply(row interface{ Scan(...any) error }) (Reply, error) {
	var (
		r        Reply
		askJSON  string
		answered int
		answer   string
		created  int64
	)
	if err := row.Scan(&r.ID, &r.CommentID, &r.Origin, &r.Kind, &r.Body, &askJSON,
		&answered, &answer, &r.AnsweredVia, &created); err != nil {
		return Reply{}, err
	}
	r.Answered = answered != 0
	r.CreatedAt = fromUnix(created)
	if r.Kind == "ask" {
		var ask Ask
		if err := json.Unmarshal([]byte(askJSON), &ask); err != nil {
			return Reply{}, fmt.Errorf("reply %d: decode ask: %w", r.ID, err)
		}
		r.Ask = &ask
		if r.Answered {
			var ans AskAnswer
			if err := json.Unmarshal([]byte(answer), &ans); err != nil {
				return Reply{}, fmt.Errorf("reply %d: decode ask answer: %w", r.ID, err)
			}
			r.AskAnswer = &ans
		}
		return r, nil
	}
	if askJSON != "" {
		return Reply{}, fmt.Errorf("reply %d: kind %q carries ask_json", r.ID, r.Kind)
	}
	r.Answer = answer
	return r, nil
}

// CreateReply inserts a reply, returning its id. When DedupKey is non-empty and
// a reply with that key already exists, no row is inserted and the existing id
// is returned with inserted=false. The insert is a single atomic upsert
// (ON CONFLICT … DO NOTHING), so concurrent redeliveries of the same reply can't
// race into a unique-constraint error — this is what makes a redelivered comment
// safe to answer twice.
func (s *Store) CreateReply(ctx context.Context, r Reply) (id int64, inserted bool, err error) {
	if (r.Kind == "ask") != (r.Ask != nil) {
		return 0, false, fmt.Errorf("create reply: kind %q with ask payload %v", r.Kind, r.Ask != nil)
	}
	askJSON := ""
	if r.Ask != nil {
		if err := r.Ask.Validate(); err != nil {
			return 0, false, fmt.Errorf("create reply: %w", err)
		}
		b, err := json.Marshal(r.Ask)
		if err != nil {
			return 0, false, fmt.Errorf("create reply: encode ask: %w", err)
		}
		askJSON = string(b)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO replies(comment_id, origin, kind, body, ask_json, answered, answer, answered_via, created_at, dedup_key)
		 VALUES(?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`,
		r.CommentID, r.Origin, r.Kind, r.Body, askJSON,
		boolInt(r.Answered), r.Answer, r.AnsweredVia, unix(time.Now()), nullString(r.DedupKey))
	if err != nil {
		return 0, false, fmt.Errorf("create reply: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n == 0 {
		// A non-NULL dedup_key already existed (a NULL key never conflicts).
		var existing int64
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM replies WHERE dedup_key=?`, r.DedupKey).Scan(&existing); err != nil {
			return 0, false, fmt.Errorf("dedup re-select: %w", err)
		}
		return existing, false, nil
	}
	id, err = res.LastInsertId()
	return id, err == nil, err
}

// GetReply returns one reply by id, or ErrNotFound.
func (s *Store) GetReply(ctx context.Context, replyID int64) (Reply, error) {
	r, err := scanReply(s.db.QueryRowContext(ctx,
		`SELECT `+replyCols+` FROM replies WHERE id=?`, replyID))
	if errors.Is(err, sql.ErrNoRows) {
		return Reply{}, fmt.Errorf("get reply %d: %w", replyID, ErrNotFound)
	}
	if err != nil {
		return Reply{}, fmt.Errorf("get reply %d: %w", replyID, err)
	}
	return r, nil
}

// ListRepliesByComment returns every reply under a comment, oldest first.
func (s *Store) ListRepliesByComment(ctx context.Context, commentID int64) ([]Reply, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+replyCols+` FROM replies WHERE comment_id=? ORDER BY created_at ASC, id ASC`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reply
	for rows.Next() {
		r, err := scanReply(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AnswerQuestion records the plain-text answer to a kind=question reply. It
// fails loud when the id doesn't exist or targets another kind.
func (s *Store) AnswerQuestion(ctx context.Context, replyID int64, answer, via string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE replies SET answered=1, answer=?, answered_via=? WHERE id=? AND kind='question'`,
		answer, via, replyID)
	if err != nil {
		return fmt.Errorf("answer question: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("answer question %d: no question reply: %w", replyID, ErrNotFound)
	}
	return nil
}

// AnswerAsk validates a structured answer against the stored ask and records
// it. Re-answering overwrites the previous answer. This is the drain path: it
// answers regardless of review status (the review is submitted by then).
func (s *Store) AnswerAsk(ctx context.Context, replyID int64, ans AskAnswer, via string) error {
	return s.answerAsk(ctx, replyID, ans, via, false)
}

// AnswerAskIfOpen is AnswerAsk for the web path: the UPDATE additionally
// requires the owning review to still be open, so an answer racing Submit
// fails with ErrReviewNotOpen instead of silently missing the frozen feedback.
func (s *Store) AnswerAskIfOpen(ctx context.Context, replyID int64, ans AskAnswer, via string) error {
	return s.answerAsk(ctx, replyID, ans, via, true)
}

func (s *Store) answerAsk(ctx context.Context, replyID int64, ans AskAnswer, via string, requireOpen bool) error {
	r, err := s.GetReply(ctx, replyID)
	if err != nil {
		return fmt.Errorf("answer ask: %w", err)
	}
	if r.Kind != "ask" {
		return fmt.Errorf("answer ask %d: reply is kind %q", replyID, r.Kind)
	}
	if err := r.Ask.ValidateAnswer(ans); err != nil {
		return fmt.Errorf("answer ask %d: %w", replyID, err)
	}
	if ans.Selected == nil {
		// The wire contract declares selected as a required array; an other-only
		// answer must serialize as [] rather than null.
		ans.Selected = []string{}
	}
	answerJSON, err := json.Marshal(ans)
	if err != nil {
		return fmt.Errorf("answer ask %d: encode answer: %w", replyID, err)
	}
	query := `UPDATE replies SET answered=1, answer=?, answered_via=? WHERE id=?`
	if requireOpen {
		query += ` AND EXISTS (
			SELECT 1 FROM comments c
			JOIN review_versions v ON v.id = c.version_id
			JOIN subjects rv ON rv.id = v.review_id
			WHERE c.id = replies.comment_id AND rv.status = 'open')`
	}
	res, err := s.db.ExecContext(ctx, query, string(answerJSON), via, replyID)
	if err != nil {
		return fmt.Errorf("answer ask %d: %w", replyID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("answer ask %d: %w", replyID, ErrReviewNotOpen)
	}
	return nil
}

// ListOpenQuestions returns Claude questions on a review that have no answer yet,
// across every version, with their comment anchor.
func (s *Store) ListOpenQuestions(ctx context.Context, reviewID string) ([]OpenQuestion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.comment_id, c.file_path, c.start_line, c.body, r.body, r.ask_json
		   FROM replies r
		   JOIN comments c ON c.id = r.comment_id
		   JOIN review_versions v ON v.id = c.version_id
		  WHERE v.review_id=? AND r.origin='claude' AND r.kind IN ('question','ask') AND r.answered=0
		  ORDER BY r.created_at ASC, r.id ASC`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpenQuestion
	for rows.Next() {
		var (
			q       OpenQuestion
			askJSON string
		)
		if err := rows.Scan(&q.ReplyID, &q.CommentID, &q.FilePath, &q.StartLine, &q.CommentBody, &q.Question, &askJSON); err != nil {
			return nil, err
		}
		if askJSON != "" {
			var ask Ask
			if err := json.Unmarshal([]byte(askJSON), &ask); err != nil {
				return nil, fmt.Errorf("open question %d: decode ask: %w", q.ReplyID, err)
			}
			q.Ask = &ask
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
