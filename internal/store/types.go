package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// Review is a code-review session keyed to a Claude window (pid) + repo root.
type Review struct {
	ID        string
	Slug      string // URL name: sanitized creation-time branch + first 8 hex of ID
	SessionID string // empty when NULL
	RepoRoot  string
	ClaudePID int    // 0 when detached (no live window owns it)
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

// AskOption is one selectable choice in an ask reply. Field names match Claude
// Code's native AskUserQuestion tool so the skill's mapping is 1:1.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// Ask is the structured payload persisted in replies.ask_json for kind=ask.
type Ask struct {
	Header      string      `json:"header,omitempty"`
	MultiSelect bool        `json:"multiSelect,omitempty"`
	Options     []AskOption `json:"options"`
}

// AskAnswer is the structured answer persisted in replies.answer for kind=ask.
type AskAnswer struct {
	Selected []string `json:"selected"`
	Other    string   `json:"other,omitempty"`
	Notes    string   `json:"notes,omitempty"`
}

// Validate rejects an ask with no options, an empty label, or duplicate labels.
func (a Ask) Validate() error {
	if len(a.Options) == 0 {
		return fmt.Errorf("ask: no options")
	}
	seen := make(map[string]bool, len(a.Options))
	for _, o := range a.Options {
		if o.Label == "" {
			return fmt.Errorf("ask: option with empty label")
		}
		if seen[o.Label] {
			return fmt.Errorf("ask: duplicate option label %q", o.Label)
		}
		seen[o.Label] = true
	}
	return nil
}

// ValidateAnswer rejects an answer that picks labels the ask never offered,
// picks more than one choice on a single-select ask, or picks nothing at all.
func (a Ask) ValidateAnswer(ans AskAnswer) error {
	offered := make(map[string]bool, len(a.Options))
	for _, o := range a.Options {
		offered[o.Label] = true
	}
	for _, label := range ans.Selected {
		if !offered[label] {
			return fmt.Errorf("ask answer: label %q was not offered", label)
		}
	}
	picks := len(ans.Selected)
	if ans.Other != "" {
		picks++
	}
	if picks == 0 {
		return fmt.Errorf("ask answer: nothing selected")
	}
	if !a.MultiSelect && picks > 1 {
		return fmt.Errorf("ask answer: %d picks on a single-select ask", picks)
	}
	return nil
}

// Reply is one turn in the thread under a comment, from Claude or the user.
type Reply struct {
	ID          int64
	CommentID   int64
	Origin      string // claude | user
	Kind        string // question | ask | clarification | note | answer
	Body        string
	Ask         *Ask // kind=ask only
	Answered    bool
	Answer      string     // kind=question plain-text answer
	AskAnswer   *AskAnswer // kind=ask answer, once answered
	AnsweredVia string     // web | askuserquestion
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
