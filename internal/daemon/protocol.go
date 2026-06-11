// Package daemon is the lazily-started local daemon: a control-plane unix-socket
// server speaking newline-delimited JSON, plus the data/UI HTTP plane it boots.
// Mirrors claude-pool's daemon (detached Setsid spawn, single-RPC-per-conn, wg
// drain on shutdown, version-skew eviction of a stale socket holder) with a
// flock guarding lazy start. Every control op is fast request/response;
// realtime delivery is the HTTP SSE stream, not a blocking socket op.
package daemon

import (
	"encoding/json"

	"github.com/yasyf/cc-review/internal/store"
)

// ProtocolVersion is stamped on every envelope for forward-compatibility.
const ProtocolVersion = 2

// Op is a control-plane request operation.
type Op string

const (
	OpHealth        Op = "health"         // liveness + version probe
	OpShutdown      Op = "shutdown"       // step down and release the socket
	OpStart         Op = "start"          // snapshot the tree, resolve/create the review
	OpResolve       Op = "resolve"        // look up an existing review (no create) for a stream consumer
	OpChannelAck    Op = "channel-ack"    // the model proves the window's channel round trip (first delivered tag seen)
	OpReply         Op = "reply"          // Claude posts questions/options/answers
	OpFeedback      Op = "feedback"       // read the frozen feedback + open questions
	OpStatus        Op = "status"         // daemon + review status
	OpSessionRecord Op = "session-record" // record SessionStart hook facts
	OpGuardEdit     Op = "guard-edit"     // PreToolUse: allow edits only once submitted

	OpFileStates         Op = "file-states"         // Claude batch-sets per-file review state
	OpUpdateAIRequest    Op = "update-ai-request"   // Claude moves an AI request through its lifecycle
	OpSubmitOrganization Op = "submit-organization" // Claude submits the version's chapter organization
	OpReviewFiles        Op = "review-files"        // Claude reads the current files + states

	OpTurnStart Op = "turn-start" // UserPromptSubmit hook: open a turn with a working-tree snapshot
	OpTurnEnd   Op = "turn-end"   // Stop hook: close the open turn with a second snapshot
)

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

// Request is one control-plane RPC.
type Request struct {
	Proto     int          `json:"proto"`
	Op        Op           `json:"op"`
	Session   string       `json:"session,omitempty"`
	ClaudePID int          `json:"claude_pid,omitempty"` // window identity: the claude ancestor pid, stamped by the client
	Cwd       string       `json:"cwd,omitempty"`
	Consumer  string       `json:"consumer,omitempty"` // stream consumer name on OpResolve (watch | channel)
	New       bool         `json:"new,omitempty"`
	Base      string       `json:"base,omitempty"` // start: explicit diff base ref for a new review (skips the trunk fallback)
	Replies   []ReplyInput `json:"replies,omitempty"`

	Files         []FileStateInput    `json:"files,omitempty"`          // file-states
	AIRequestID   int64               `json:"ai_request_id,omitempty"`  // file-states (optional) | update-ai-request
	AIStatus      string              `json:"ai_status,omitempty"`      // update-ai-request: working | done | failed
	Summary       string              `json:"summary,omitempty"`        // update-ai-request
	Unmatched     []store.Unmatched   `json:"unmatched,omitempty"`      // update-ai-request
	Organization  *store.Organization `json:"organization,omitempty"`   // submit-organization
	VersionNumber int                 `json:"version_number,omitempty"` // submit-organization: required, must match the current version

	Prompt         string `json:"prompt,omitempty"`          // turn-start: the submitted user prompt (stored as a capped excerpt)
	TranscriptPath string `json:"transcript_path,omitempty"` // turn-start: the session transcript file at prompt time
}

// Response is one control-plane reply.
type Response struct {
	Proto         int             `json:"proto"`
	OK            bool            `json:"ok"`
	Error         string          `json:"error,omitempty"`
	DaemonVersion string          `json:"daemon_version,omitempty"`
	URL           string          `json:"url,omitempty"`
	ReviewID      string          `json:"review_id,omitempty"`
	Version       int             `json:"version,omitempty"`
	Resumed       bool            `json:"resumed,omitempty"`
	HTTPPort      int             `json:"http_port,omitempty"`
	FeedbackPath  string          `json:"feedback_path,omitempty"`
	Feedback      json.RawMessage `json:"feedback,omitempty"`
	Status        string          `json:"status,omitempty"`
	ChannelState  string          `json:"channel_state,omitempty"` // start: active|pending|inactive — active only for a proven window with the channel consumer attached
	Allow         bool            `json:"allow,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	ReviewFiles   json.RawMessage `json:"review_files,omitempty"` // review-files: {version_number, files: [...], organization?: {basis_version, overview, chapters, new_paths}}
	AIRequest     json.RawMessage `json:"ai_request,omitempty"`   // start: the eager system organize request (absent on an unchanged resume)
}
