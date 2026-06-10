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
	ID        string   `json:"id"`
	CommentID string   `json:"commentId"`
	Origin    string   `json:"origin"`
	Kind      string   `json:"kind"`
	Body      string   `json:"body"`
	Options   []string `json:"options,omitempty"`
	CreatedAt string   `json:"createdAt"`
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

// ToReview converts a store review, taking the branch from the active version
// (branch lives on the version, not the review).
func ToReview(r store.Review, branch string) Review {
	return Review{ID: r.ID, Status: r.Status, RepoRoot: r.RepoRoot, Branch: branch, CreatedAt: iso(r.CreatedAt)}
}

// ToReply converts a store reply.
func ToReply(r store.Reply) Reply {
	return Reply{
		ID: id(r.ID), CommentID: id(r.CommentID), Origin: r.Origin, Kind: r.Kind, Body: r.Body,
		Options: DecodeOptions(r.OptionsJSON), CreatedAt: iso(r.CreatedAt),
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

// DecodeOptions parses a stored options_json array, returning nil when empty.
func DecodeOptions(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func id(n int64) string      { return strconv.FormatInt(n, 10) }
func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }
