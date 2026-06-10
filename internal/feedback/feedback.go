// Package feedback builds, freezes, and loads the queryable JSON snapshot of a
// review's threads produced when the human presses Submit. The same structure is
// written by the HTTP submit handler and read back by the daemon's feedback
// command, so both sides agree on the shape.
package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/yasyf/cc-review/internal/store"
)

// Reply is one turn under a comment, frozen.
type Reply struct {
	ID          int64            `json:"id"`
	Origin      string           `json:"origin"`
	Kind        string           `json:"kind"`
	Body        string           `json:"body,omitempty"`
	Ask         *store.Ask       `json:"ask,omitempty"`
	Answered    bool             `json:"answered"`
	Answer      string           `json:"answer,omitempty"`
	AskAnswer   *store.AskAnswer `json:"ask_answer,omitempty"`
	AnsweredVia string           `json:"answered_via,omitempty"`
}

// Thread is a comment plus its replies.
type Thread struct {
	CommentID   int64   `json:"comment_id"`
	FilePath    string  `json:"file_path"`
	Side        string  `json:"side"`
	StartLine   int     `json:"start_line"`
	EndLine     int     `json:"end_line"`
	LineContent string  `json:"line_content,omitempty"`
	Body        string  `json:"body"`
	Status      string  `json:"status"`
	Replies     []Reply `json:"replies"`
}

// OpenQuestion is a Claude question still awaiting an answer at submit time.
type OpenQuestion struct {
	ReplyID     int64      `json:"reply_id"`
	CommentID   int64      `json:"comment_id"`
	FilePath    string     `json:"file_path"`
	StartLine   int        `json:"start_line"`
	CommentBody string     `json:"comment_body"`
	Question    string     `json:"question"`
	Ask         *store.Ask `json:"ask,omitempty"`
}

// Feedback is the full frozen snapshot for one version of a review.
type Feedback struct {
	ReviewID      string         `json:"review_id"`
	Version       int            `json:"version"`
	FrozenAt      int64          `json:"frozen_at"`
	Threads       []Thread       `json:"threads"`
	OpenQuestions []OpenQuestion `json:"open_questions"`
}

// Build assembles the feedback snapshot for a version from the store.
func Build(ctx context.Context, st *store.Store, reviewID string, version store.Version, frozenAt time.Time) (Feedback, error) {
	comments, err := st.ListCommentsByVersion(ctx, version.ID)
	if err != nil {
		return Feedback{}, fmt.Errorf("list comments: %w", err)
	}
	threads := make([]Thread, 0, len(comments))
	for _, c := range comments {
		replies, err := st.ListRepliesByComment(ctx, c.ID)
		if err != nil {
			return Feedback{}, fmt.Errorf("list replies: %w", err)
		}
		threads = append(threads, Thread{
			CommentID: c.ID, FilePath: c.FilePath, Side: c.Side, StartLine: c.StartLine,
			EndLine: c.EndLine, LineContent: c.LineContent, Body: c.Body, Status: c.Status,
			Replies: toReplies(replies),
		})
	}
	open, err := st.ListOpenQuestions(ctx, reviewID)
	if err != nil {
		return Feedback{}, fmt.Errorf("list open questions: %w", err)
	}
	questions := make([]OpenQuestion, 0, len(open))
	for _, q := range open {
		questions = append(questions, OpenQuestion{
			ReplyID: q.ReplyID, CommentID: q.CommentID, FilePath: q.FilePath, StartLine: q.StartLine,
			CommentBody: q.CommentBody, Question: q.Question, Ask: q.Ask,
		})
	}
	return Feedback{
		ReviewID: reviewID, Version: version.VersionNumber, FrozenAt: frozenAt.Unix(),
		Threads: threads, OpenQuestions: questions,
	}, nil
}

// Freeze writes the snapshot to path (0600), creating nothing else.
func Freeze(path string, f Feedback) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal feedback: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write feedback %s: %w", path, err)
	}
	return nil
}

// Load reads a frozen snapshot from path.
func Load(path string) (Feedback, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Feedback{}, fmt.Errorf("read feedback %s: %w", path, err)
	}
	var f Feedback
	if err := json.Unmarshal(b, &f); err != nil {
		return Feedback{}, fmt.Errorf("unmarshal feedback: %w", err)
	}
	return f, nil
}

func toReplies(in []store.Reply) []Reply {
	out := make([]Reply, 0, len(in))
	for _, r := range in {
		out = append(out, Reply{
			ID: r.ID, Origin: r.Origin, Kind: r.Kind, Body: r.Body, Ask: r.Ask,
			Answered: r.Answered, Answer: r.Answer, AskAnswer: r.AskAnswer, AnsweredVia: r.AnsweredVia,
		})
	}
	return out
}
