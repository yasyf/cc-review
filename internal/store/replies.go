package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const replyCols = `id, comment_id, origin, kind, body, options_json, answered, answer, answered_via, created_at`

// OpenQuestion is a Claude question awaiting an answer, with enough comment
// context to surface it in the feedback drain.
type OpenQuestion struct {
	ReplyID     int64
	CommentID   int64
	FilePath    string
	StartLine   int
	CommentBody string
	Question    string
	OptionsJSON string
}

func scanReply(row interface{ Scan(...any) error }) (Reply, error) {
	var (
		r        Reply
		answered int
		created  int64
	)
	if err := row.Scan(&r.ID, &r.CommentID, &r.Origin, &r.Kind, &r.Body, &r.OptionsJSON,
		&answered, &r.Answer, &r.AnsweredVia, &created); err != nil {
		return Reply{}, err
	}
	r.Answered = answered != 0
	r.CreatedAt = fromUnix(created)
	return r, nil
}

// CreateReply inserts a reply, returning its id. When DedupKey is non-empty and
// a reply with that key already exists, no row is inserted and the existing id
// is returned with inserted=false (the single serialized writer makes the
// check-then-insert atomic). This is what makes a redelivered comment safe to
// answer twice.
func (s *Store) CreateReply(ctx context.Context, r Reply) (id int64, inserted bool, err error) {
	if r.DedupKey != "" {
		var existing int64
		err := s.db.QueryRowContext(ctx, `SELECT id FROM replies WHERE dedup_key=?`, r.DedupKey).Scan(&existing)
		if err == nil {
			return existing, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, false, fmt.Errorf("dedup lookup: %w", err)
		}
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO replies(comment_id, origin, kind, body, options_json, answered, answer, answered_via, created_at, dedup_key)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		r.CommentID, r.Origin, r.Kind, r.Body, defaultStr(r.OptionsJSON, "[]"),
		boolInt(r.Answered), r.Answer, r.AnsweredVia, unix(time.Now()), nullString(r.DedupKey))
	if err != nil {
		return 0, false, fmt.Errorf("create reply: %w", err)
	}
	id, err = res.LastInsertId()
	return id, err == nil, err
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

// AnswerReply records the answer to a Claude question and how it was answered.
func (s *Store) AnswerReply(ctx context.Context, replyID int64, answer, via string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE replies SET answered=1, answer=?, answered_via=? WHERE id=?`, answer, via, replyID)
	if err != nil {
		return fmt.Errorf("answer reply: %w", err)
	}
	return nil
}

// ListOpenQuestions returns Claude questions on a review that have no answer yet,
// across every version, with their comment anchor.
func (s *Store) ListOpenQuestions(ctx context.Context, reviewID string) ([]OpenQuestion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.comment_id, c.file_path, c.start_line, c.body, r.body, r.options_json
		   FROM replies r
		   JOIN comments c ON c.id = r.comment_id
		   JOIN review_versions v ON v.id = c.version_id
		  WHERE v.review_id=? AND r.origin='claude' AND r.kind IN ('question','option') AND r.answered=0
		  ORDER BY r.created_at ASC, r.id ASC`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpenQuestion
	for rows.Next() {
		var q OpenQuestion
		if err := rows.Scan(&q.ReplyID, &q.CommentID, &q.FilePath, &q.StartLine, &q.CommentBody, &q.Question, &q.OptionsJSON); err != nil {
			return nil, err
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
