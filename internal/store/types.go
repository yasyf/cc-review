package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/yasyf/cc-interact/vcs"
)

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

// Files decodes the version's files_json summary.
func (v Version) Files() ([]vcs.FileChange, error) {
	var files []vcs.FileChange
	if err := json.Unmarshal([]byte(v.FilesJSON), &files); err != nil {
		return nil, fmt.Errorf("version %d: decode files: %w", v.ID, err)
	}
	return files, nil
}

// ClassifiedFile is a version file widened with advisory generated/vendored
// flags. It embeds vcs.FileChange so the path/old_path/status/fingerprint tags
// pass through unchanged; omitempty keeps the files_json bytes identical to the
// plain FileChange encoding when both flags are false.
type ClassifiedFile struct {
	vcs.FileChange
	Generated bool `json:"generated,omitempty"`
	Vendored  bool `json:"vendored,omitempty"`
}

// FileFlags decodes files_json as the classified superset, reading the
// generated/vendored flags alongside the base file fields. A key-less blob
// (written before classification existed) yields zero-value flags.
func (v Version) FileFlags() ([]ClassifiedFile, error) {
	var files []ClassifiedFile
	if err := json.Unmarshal([]byte(v.FilesJSON), &files); err != nil {
		return nil, fmt.Errorf("version %d: decode file flags: %w", v.ID, err)
	}
	return files, nil
}

// FileState is one file's review-scoped state: hidden persists across
// versions; reviewed survives exactly while ReviewedFingerprint matches the
// file's current diff fingerprint.
type FileState struct {
	ReviewID            string
	Path                string
	Reviewed            bool
	Hidden              bool
	ReviewedFingerprint string
	UpdatedAt           time.Time
}

// FileStateInput is one file's partial state change: a nil flag keeps the
// current value.
type FileStateInput struct {
	Path     string
	Reviewed *bool
	Hidden   *bool
}

// PriorState snapshots a file's state before an AI batch, for undo.
type PriorState struct {
	Reviewed    bool   `json:"reviewed"`
	Hidden      bool   `json:"hidden"`
	Fingerprint string `json:"fingerprint"`
}

// AppliedState is a file's absolute state after an AI batch.
type AppliedState struct {
	Reviewed bool `json:"reviewed"`
	Hidden   bool `json:"hidden"`
}

// FileStateResult is one file's prior and applied state from ApplyFileStates.
type FileStateResult struct {
	Path    string
	Prior   PriorState
	Applied AppliedState
}

// AIChange records one file's transition under an AI request; the first prior
// per path is kept across batches so undo restores the pre-request state.
type AIChange struct {
	Path    string       `json:"path"`
	Reason  string       `json:"reason"`
	Prior   PriorState   `json:"prior"`
	Applied AppliedState `json:"applied"`
}

// Unmatched is one part of an AI-bar prompt Claude did not act on, and why.
type Unmatched struct {
	Pattern string `json:"pattern"`
	Why     string `json:"why"`
}

// AIQuestion is a clarifying question the agent parks a request on (status
// awaiting_input): a body and, optionally, structured options mirroring an Ask.
type AIQuestion struct {
	Body string `json:"body"`
	Ask  *Ask   `json:"ask,omitempty"`
}

// AIAnswer is the human's reply to an AIQuestion: free text, or a structured
// AskAnswer when the question carried an Ask.
type AIAnswer struct {
	Text      string     `json:"text,omitempty"`
	AskAnswer *AskAnswer `json:"askAnswer,omitempty"`
}

// AIRequest is one AI-bar (source=user) or auto-organize (source=system)
// request and its lifecycle.
type AIRequest struct {
	ID            int64
	ReviewID      string
	VersionNumber int
	Source        string // user | system
	Prompt        string
	Status        string // pending | working | awaiting_input | answered | done | failed | undone
	Summary       string
	Phase         string // free-text working-step label streamed live, e.g. "reading 8 files…"
	Unmatched     []Unmatched
	Changes       []AIChange
	Question      *AIQuestion // set while awaiting_input
	Answer        *AIAnswer   // set once answered
	Attempt       int         // bumped each time an answer re-opens the request
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ChapterFile is one file within a chapter, rated by the danger of skimming it.
type ChapterFile struct {
	Path      string     `json:"path"`
	Risk      string     `json:"risk"`      // high | medium | low | mechanical
	Rationale string     `json:"rationale"` // why this file is here
	Focus     string     `json:"focus"`     // what to scrutinize here and why it carries this risk
	Lines     []LineNote `json:"lines"`     // importance ranges over NEW-side added lines; empty => renders normally
}

// LineNote marks an importance range of added (NEW-side) lines within a file,
// anchored 1-based like comments and turn attributions: focus lines get full
// weight plus a gutter dot, mechanical lines are dimmed most.
type LineNote struct {
	Start int    `json:"start"` // first added line (NEW-side, 1-based), inclusive
	End   int    `json:"end"`   // last added line, inclusive
	Level string `json:"level"` // focus | mechanical
	Note  string `json:"note"`  // hover-bubble hint
}

// Chapter is one ordered story beat of a review.
type Chapter struct {
	Title   string        `json:"title"`
	Summary string        `json:"summary"`
	Files   []ChapterFile `json:"files"`
}

// Organization is Claude's chaptering of one version's diff. The camelCase
// tags pass straight through to the wire like Ask.
type Organization struct {
	Overview *string   `json:"overview"`
	Chapters []Chapter `json:"chapters"`
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

// Annotation is a Claude-authored line-range highlight on a version's diff: an
// informational mark (not a comment thread) the agent adds on an AI-bar request.
// AIRequestID ties it to the request that created it, so undo can remove it.
type Annotation struct {
	ID          int64
	VersionID   int64
	FilePath    string
	Side        string // additions | deletions
	StartLine   int
	EndLine     int
	Label       string
	AIRequestID int64
	CreatedAt   time.Time
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
