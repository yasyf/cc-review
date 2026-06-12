// Package wire is the JSON contract shared by the HTTP REST responses, the SSE
// event payloads, and the daemon's Claude-reply events. It converts the store's
// internal types (int64 ids, unix timestamps, PascalCase) into the camelCase,
// string-id shape the SPA's TypeScript types expect, and builds the tagged-union
// event frames the browser and the Claude-side stream consumers parse.
package wire

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/store"
)

// Review is the SPA's view of a review.
type Review struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	RepoRoot  string `json:"repoRoot"`
	Branch    string `json:"branch"`
	CreatedAt string `json:"createdAt"`
}

// LineRange is a comment's anchored line span.
type LineRange struct {
	Start     int    `json:"start"`
	End       int    `json:"end"`
	StartSide string `json:"startSide,omitempty"`
	EndSide   string `json:"endSide,omitempty"`
}

// Reply is one turn under a comment.
type Reply struct {
	ID          string           `json:"id"`
	CommentID   string           `json:"commentId"`
	Origin      string           `json:"origin"`
	Kind        string           `json:"kind"`
	Body        string           `json:"body"`
	Ask         *store.Ask       `json:"ask,omitempty"`
	Answered    bool             `json:"answered,omitempty"`
	AskAnswer   *store.AskAnswer `json:"askAnswer,omitempty"`
	AnsweredVia string           `json:"answeredVia,omitempty"`
	CreatedAt   string           `json:"createdAt"`
}

// Comment is an inline comment with its thread.
type Comment struct {
	ID          string    `json:"id"`
	VersionID   string    `json:"versionId"`
	FilePath    string    `json:"filePath"`
	Side        string    `json:"side"`
	Range       LineRange `json:"range"`
	LineContent string    `json:"lineContent"`
	Body        string    `json:"body"`
	Origin      string    `json:"origin"`
	Status      string    `json:"status"`
	CreatedAt   string    `json:"createdAt"`
	Replies     []Reply   `json:"replies"`
}

// VersionSummary is one entry in the version list.
type VersionSummary struct {
	VersionID string `json:"versionId"`
	Version   int    `json:"version"`
	Branch    string `json:"branch"`
	BaseRef   string `json:"baseRef"`
	CreatedAt string `json:"createdAt"`
}

// FileState is the SPA's view of one file's review state.
type FileState struct {
	Reviewed bool `json:"reviewed"`
	Hidden   bool `json:"hidden"`
}

// AIRequest is the SPA's view of an AI-bar or auto-organize request.
type AIRequest struct {
	ID        string            `json:"id"`
	Source    string            `json:"source"`
	Prompt    string            `json:"prompt"`
	Status    string            `json:"status"`
	Summary   string            `json:"summary"`
	Unmatched []store.Unmatched `json:"unmatched"`
	Changes   []store.AIChange  `json:"changes"`
	CreatedAt string            `json:"createdAt"`
	UpdatedAt string            `json:"updatedAt"`
}

// Turn is the SPA's view of one Claude prompt→stop window.
type Turn struct {
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	PromptExcerpt string `json:"prompt"`
	Interrupted   bool   `json:"interrupted"`
	StartedAt     int64  `json:"startedAt"`
	EndedAt       int64  `json:"endedAt"`
}

// AttributionRange is the SPA's view of one attributed line span; an omitted
// turnId means unattributed.
type AttributionRange struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	TurnID string `json:"turnId,omitempty"`
}

// Decision is the SPA's view of one decision-ledger row inside a turn window:
// the turn-activity panel data.
type Decision struct {
	TsMs     int64  `json:"tsMs"`
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	Action   string `json:"action"`
	ToolName string `json:"toolName"`
	Message  string `json:"message"`
}

// ToReview converts a store review, taking the branch from the active version
// (branch lives on the version, not the review).
func ToReview(r store.Review, branch string) Review {
	return Review{ID: r.ID, Status: r.Status, RepoRoot: r.RepoRoot, Branch: branch, CreatedAt: iso(r.CreatedAt)}
}

// ToReply converts a store reply. The store already decoded ask_json and the
// structured answer, so this is an infallible copy.
func ToReply(r store.Reply) Reply {
	return Reply{
		ID: id(r.ID), CommentID: id(r.CommentID), Origin: r.Origin, Kind: r.Kind, Body: r.Body,
		Ask: r.Ask, Answered: r.Answered, AskAnswer: r.AskAnswer, AnsweredVia: r.AnsweredVia,
		CreatedAt: iso(r.CreatedAt),
	}
}

// ToComment converts a store comment plus its replies. Replies is always a
// non-nil array so the SPA can map over it unconditionally.
func ToComment(c store.Comment, replies []store.Reply) Comment {
	out := Comment{
		ID: id(c.ID), VersionID: id(c.VersionID), FilePath: c.FilePath, Side: c.Side,
		Range:       LineRange{Start: c.StartLine, End: c.EndLine, StartSide: c.StartSide, EndSide: c.EndSide},
		LineContent: c.LineContent, Body: c.Body, Origin: c.Author, Status: c.Status,
		CreatedAt: iso(c.CreatedAt), Replies: make([]Reply, 0, len(replies)),
	}
	for _, r := range replies {
		out.Replies = append(out.Replies, ToReply(r))
	}
	return out
}

// ToAIRequest converts a store AI request. Unmatched and Changes are always
// non-nil arrays so the SPA can map over them unconditionally.
func ToAIRequest(r store.AIRequest) AIRequest {
	out := AIRequest{
		ID: id(r.ID), Source: r.Source, Prompt: r.Prompt, Status: r.Status, Summary: r.Summary,
		Unmatched: r.Unmatched, Changes: r.Changes, CreatedAt: iso(r.CreatedAt), UpdatedAt: iso(r.UpdatedAt),
	}
	if out.Unmatched == nil {
		out.Unmatched = []store.Unmatched{}
	}
	if out.Changes == nil {
		out.Changes = []store.AIChange{}
	}
	return out
}

// ToTurn converts a store turn.
func ToTurn(t store.Turn) Turn {
	return Turn{
		ID: id(t.ID), SessionID: t.SessionID, PromptExcerpt: t.PromptExcerpt,
		Interrupted: t.Status == "interrupted", StartedAt: t.StartedAt, EndedAt: t.EndedAt,
	}
}

// ToDecision converts a decision-ledger row.
func ToDecision(d decisions.Decision) Decision {
	return Decision{
		TsMs: d.TsMs, Source: d.Source, Kind: d.Kind, Action: d.Action,
		ToolName: d.ToolName, Message: d.Message,
	}
}

// ToAttributionRange converts a store attribution range; a zero TurnID becomes
// an omitted turnId.
func ToAttributionRange(r store.AttributionRange) AttributionRange {
	out := AttributionRange{Start: r.Start, End: r.End}
	if r.TurnID != 0 {
		out.TurnID = id(r.TurnID)
	}
	return out
}

// ToVersionSummary converts a store version.
func ToVersionSummary(v store.Version) VersionSummary {
	return VersionSummary{
		VersionID: id(v.ID), Version: v.VersionNumber, Branch: v.Branch, BaseRef: v.BaseRef, CreatedAt: iso(v.CreatedAt),
	}
}

// Event builds a tagged-union event frame: {type, version_number, ...fields}.
// The full frame is what both the browser (JSON.parse on the SSE data) and the
// Claude-side stream consumer read, so it must be self-contained.
func Event(typ string, version int, fields map[string]any) []byte {
	m := map[string]any{"type": typ, "version_number": version}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func id(n int64) string      { return strconv.FormatInt(n, 10) }
func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }
