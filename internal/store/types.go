package store

import (
	"encoding/json"
	"time"
)

// Review is a code-review session keyed to a Claude session id + repo root.
type Review struct {
	ID        string
	SessionID string // empty when NULL (repo-root-only, pre-backfill)
	RepoRoot  string
	Status    string // open | submitted | closed
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Version is one snapshot of the working tree under a review.
type Version struct {
	ID            int64
	ReviewID      string
	VersionNumber int
	Branch        string
	BaseRef       string
	PatchPath     string
	FilesJSON     string
	CreatedAt     time.Time
}

// Comment is an inline comment anchored to a line range of a version's diff.
type Comment struct {
	ID          int64
	VersionID   int64
	FilePath    string
	Side        string // additions | deletions
	StartLine   int
	EndLine     int
	StartSide   string
	EndSide     string
	LineContent string
	Body        string
	Author      string // user | claude
	Status      string // open | resolved
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Reply is one turn in the thread under a comment, from Claude or the user.
type Reply struct {
	ID          int64
	CommentID   int64
	Origin      string // claude | user
	Kind        string // question | option | clarification | note | answer
	Body        string
	OptionsJSON string
	Answered    bool
	Answer      string
	AnsweredVia string // web | askuserquestion
	CreatedAt   time.Time
	DedupKey    string // empty => no dedup
}

// Event is one entry in a review's append-only log, fanned out to every consumer.
type Event struct {
	ReviewID      string
	Seq           int64
	Origin        string // user | claude | system
	Type          string
	VersionNumber int
	Payload       json.RawMessage
	CreatedAt     time.Time
	DedupKey      string // empty => no dedup
}

// SessionHook records what the SessionStart hook reported for a Claude session.
type SessionHook struct {
	SessionID      string
	Cwd            string
	TranscriptPath string
	StartedAt      time.Time
}
