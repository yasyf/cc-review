// Package daemon wires cc-review's review domain onto cc-interact's daemon
// substrate: it builds the daemon.Config (scope = repo root, gate = block while a
// review is open, version-stamped channel presence), registers the review ops as
// handlers, mounts the REST plane, and runs the stale-request sweeper. ReviewClient
// is the typed control client the CLI and the channel tools speak through.
package daemon

import (
	"encoding/json"

	ccd "github.com/yasyf/cc-interact/daemon"

	"github.com/yasyf/cc-review/internal/store"
)

// Op is a review control-plane operation, registered on the daemon by name.
const (
	OpStart              ccd.Op = "start"
	OpReply              ccd.Op = "reply"
	OpFeedback           ccd.Op = "feedback"
	OpFileStates         ccd.Op = "file-states"
	OpFileStatesByRisk   ccd.Op = "file-states-by-risk"
	OpUpdateAIRequest    ccd.Op = "update-ai-request"
	OpSubmitOrganization ccd.Op = "submit-organization"
	OpReviewFiles        ccd.Op = "review-files"
	OpAnnotate           ccd.Op = "annotate"
	OpTurnStart          ccd.Op = "turn-start"
	OpTurnEnd            ccd.Op = "turn-end"
)

// AnnotateInput is one annotation Claude adds to the diff: kind "highlight"
// marks a line range as a non-blocking informational highlight; kind "comment"
// opens a Claude-authored comment thread on the range. Body is the highlight's
// label or the comment's text.
type AnnotateInput struct {
	Kind      string `json:"kind"`
	FilePath  string `json:"file_path"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Body      string `json:"body"`
}

// ReplyInput is one reply Claude posts. A non-zero AnswerTo answers an existing
// question or ask (post-submit drain); otherwise it is a new Claude reply of
// Kind under CommentID. Ask rides with kind=ask; AskAnswer answers an ask
// target, Answer a plain question target.
type ReplyInput struct {
	CommentID int64            `json:"comment_id,omitempty"`
	Kind      string           `json:"kind,omitempty"` // question | ask | clarification
	Body      string           `json:"body,omitempty"`
	Ask       *store.Ask       `json:"ask,omitempty"`
	Answer    string           `json:"answer,omitempty"`
	AskAnswer *store.AskAnswer `json:"ask_answer,omitempty"`
	AnswerTo  int64            `json:"answer_to,omitempty"`
	DedupKey  string           `json:"dedup_key,omitempty"`
}

// FileStateInput is one file's partial state change: a nil flag keeps the
// current value. Reason is carried into the file.states event and the AI
// request's change record, never stored on the file itself.
type FileStateInput struct {
	Path     string `json:"path"`
	Reviewed *bool  `json:"reviewed,omitempty"`
	Hidden   *bool  `json:"hidden,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ReviewFilesFilter narrows the inline file subset get_review_files returns; the
// on-disk review_files_path always carries the full list. A nil bool leaves that
// dimension unfiltered.
type ReviewFilesFilter struct {
	Status   string
	Reviewed *bool
	Hidden   *bool
}

// body is the domain payload carried in an Envelope.Body; each handler reads
// only the fields its op uses. The window (session, pid) and scope (cwd) ride on
// the envelope itself, never here.
type body struct {
	New           bool                `json:"new,omitempty"`            // start
	Base          string              `json:"base,omitempty"`           // start
	Replies       []ReplyInput        `json:"replies,omitempty"`        // reply
	Files         []FileStateInput    `json:"files,omitempty"`          // file-states
	Annotations   []AnnotateInput     `json:"annotations,omitempty"`    // annotate
	Risk          []string            `json:"risk,omitempty"`           // file-states-by-risk
	Reason        string              `json:"reason,omitempty"`         // file-states-by-risk
	AIRequestID   int64               `json:"ai_request_id,omitempty"`  // file-states (optional) | file-states-by-risk | update-ai-request
	AIStatus      string              `json:"ai_status,omitempty"`      // update-ai-request (tool param "status"; not the review-files Status filter below)
	Summary       string              `json:"summary,omitempty"`        // update-ai-request
	Phase         string              `json:"phase,omitempty"`          // update-ai-request
	Unmatched     []store.Unmatched   `json:"unmatched,omitempty"`      // update-ai-request
	Question      string              `json:"question,omitempty"`       // update-ai-request (awaiting_input)
	Ask           *store.Ask          `json:"ask,omitempty"`            // update-ai-request (awaiting_input)
	Organization  *store.Organization `json:"organization,omitempty"`   // submit-organization
	VersionNumber int                 `json:"version_number,omitempty"` // submit-organization
	Partial       bool                `json:"partial,omitempty"`        // submit-organization (streaming)
	Status        string              `json:"status,omitempty"`         // review-files (filter)
	Reviewed      *bool               `json:"reviewed,omitempty"`       // review-files (filter) | file-states-by-risk (target)
	Hidden        *bool               `json:"hidden,omitempty"`         // review-files (filter) | file-states-by-risk (target)
	Prompt        string              `json:"prompt,omitempty"`         // turn-start
}

// result is the domain payload a handler returns in Reply.Body. Envelope-level
// outputs (review id, status, http port) ride on the Reply itself.
type result struct {
	URL          string            `json:"url,omitempty"`           // start
	Version      int               `json:"version,omitempty"`       // start
	Resumed      bool              `json:"resumed,omitempty"`       // start
	ChannelState string            `json:"channel_state,omitempty"` // start
	AIRequests   []json.RawMessage `json:"ai_requests,omitempty"`   // start
	FeedbackPath string            `json:"feedback_path,omitempty"` // feedback
	Feedback     json.RawMessage   `json:"feedback,omitempty"`      // feedback
	ReviewFiles  json.RawMessage   `json:"review_files,omitempty"`  // review-files
	Paths        []string          `json:"paths,omitempty"`         // file-states-by-risk
}

func decodeBody(raw json.RawMessage) body {
	var b body
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &b)
	}
	return b
}

func okReply(r result) ccd.Reply {
	raw, _ := json.Marshal(r)
	return ccd.Reply{OK: true, Body: raw}
}

func errReply(msg string) ccd.Reply { return ccd.Reply{OK: false, Error: msg} }
