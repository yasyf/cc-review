package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/yasyf/cc-interact/channel"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/store"
)

// channelNotifyMethod is the JSON-RPC method each subject event is pushed under
// on the Claude channel.
const channelNotifyMethod = "notifications/claude/channel"

// channelTools advertises cc-review's five review tools to the agent's MCP
// channel. The handlers round-trip to the daemon via ReviewClient because the
// channel server is a separate stdio process and cannot touch the store directly.
func channelTools(_ context.Context, session, scope string) ([]channel.Tool, string, error) {
	rc := daemon.NewReviewClient()
	tools := []channel.Tool{
		{
			Name:        "reply",
			Description: "Post a question, structured ask, or clarification under a cc-review comment, or answer one.",
			InputSchema: replyToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage) (string, bool) {
				var in daemon.ReplyInput
				if err := json.Unmarshal(args, &in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				if err := rc.Reply(ctx, session, scope, []daemon.ReplyInput{in}); err != nil {
					return err.Error(), true
				}
				return "ok", false
			},
		},
		{
			Name:        "set_file_states",
			Description: "Batch-set per-file review state (reviewed/hidden) on the open cc-review. ai_request_id ties the batch to an AI request as one undoable unit.",
			InputSchema: setFileStatesToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage) (string, bool) {
				var in struct {
					Files       []daemon.FileStateInput `json:"files"`
					AIRequestID string                  `json:"ai_request_id"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				id, err := parseAIRequestID(in.AIRequestID, false)
				if err != nil {
					return err.Error(), true
				}
				if err := rc.FileStates(ctx, session, scope, in.Files, id); err != nil {
					return err.Error(), true
				}
				return "ok", false
			},
		},
		{
			Name:        "update_ai_request",
			Description: "Move an AI request through its lifecycle: working when you start, done or failed when you finish (with a summary and any unmatched prompt parts).",
			InputSchema: updateAIRequestToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage) (string, bool) {
				var in struct {
					AIRequestID string            `json:"ai_request_id"`
					Status      string            `json:"status"`
					Summary     string            `json:"summary"`
					Unmatched   []store.Unmatched `json:"unmatched"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				id, err := parseAIRequestID(in.AIRequestID, true)
				if err != nil {
					return err.Error(), true
				}
				if err := rc.UpdateAIRequest(ctx, session, scope, id, in.Status, in.Summary, in.Unmatched); err != nil {
					return err.Error(), true
				}
				return "ok", false
			},
		},
		{
			Name:        "submit_organization",
			Description: "Submit the review's chapter organization: every changed file in exactly one chapter, each rated by the risk of skimming it. Chapter order is narrative; file order within a chapter is rank, scariest first. On resubmit keep every entry the new information doesn't touch byte-identical — the UI animates only what moved. A stale version_number is rejected with the current one.",
			InputSchema: submitOrganizationToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage) (string, bool) {
				var in struct {
					Overview      *string         `json:"overview"`
					VersionNumber int             `json:"version_number"`
					Chapters      []store.Chapter `json:"chapters"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				org := store.Organization{Overview: in.Overview, Chapters: in.Chapters}
				if err := rc.SubmitOrganization(ctx, session, scope, org, in.VersionNumber); err != nil {
					return err.Error(), true
				}
				return "ok", false
			},
		},
		{
			Name:        "get_review_files",
			Description: "List the open cc-review's current version number, patch_path (the on-disk unified diff for the current version), and files with status and review state — the server truth bulk operations must act on. Includes the latest organization (overview + chapters) with basis_version, per-file delta (changed/moved/removed; absent = unchanged), and new_paths.",
			InputSchema: getReviewFilesToolSchema(),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, bool) {
				raw, err := rc.ReviewFiles(ctx, session, scope)
				if err != nil {
					return err.Error(), true
				}
				return string(raw), false
			},
		},
	}
	return tools, channelNotifyMethod, nil
}

func parseAIRequestID(raw string, required bool) (int64, error) {
	if raw == "" {
		if required {
			return 0, errors.New("ai_request_id required")
		}
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad ai_request_id %q: %w", raw, err)
	}
	return id, nil
}

func replyToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"comment_id": map[string]any{"type": "integer", "description": "comment id to reply under"},
			"kind":       map[string]any{"type": "string", "enum": []string{"question", "ask", "clarification"}, "description": "required for new replies; omit only with answer_to"},
			"body":       map[string]any{"type": "string", "description": "reply text (the question for kind=ask)"},
			"ask": map[string]any{
				"type":        "object",
				"description": "structured options for kind=ask, mirroring AskUserQuestion",
				"properties": map[string]any{
					"header":      map[string]any{"type": "string", "description": "short chip, e.g. Approach"},
					"multiSelect": map[string]any{"type": "boolean"},
					"options": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label":       map[string]any{"type": "string"},
								"description": map[string]any{"type": "string"},
								"preview":     map[string]any{"type": "string", "description": "markdown/code shown when the option is focused"},
							},
							"required": []string{"label"},
						},
					},
				},
				"required": []string{"options"},
			},
			"answer": map[string]any{"type": "string", "description": "answer text for a plain question target"},
			"ask_answer": map[string]any{
				"type":        "object",
				"description": "answer for an ask target",
				"properties": map[string]any{
					"selected": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"other":    map[string]any{"type": "string"},
					"notes":    map[string]any{"type": "string"},
				},
				"required": []string{"selected"},
			},
			"answer_to": map[string]any{"type": "integer", "description": "reply id of the question or ask being answered"},
		},
	}
}

func setFileStatesToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"files": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":     map[string]any{"type": "string"},
						"reviewed": map[string]any{"type": "boolean"},
						"hidden":   map[string]any{"type": "boolean"},
						"reason":   map[string]any{"type": "string", "description": "one line: why this state"},
					},
					"required": []string{"path"},
				},
			},
			"ai_request_id": map[string]any{"type": "string", "description": "id of the AI request these changes belong to"},
		},
		"required": []string{"files"},
	}
}

func updateAIRequestToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ai_request_id": map[string]any{"type": "string"},
			"status":        map[string]any{"type": "string", "enum": []string{"working", "done", "failed"}},
			"summary":       map[string]any{"type": "string", "description": "one sentence: what you did and why"},
			"unmatched": map[string]any{
				"type":        "array",
				"description": "parts of the prompt you did not act on, and why",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern": map[string]any{"type": "string"},
						"why":     map[string]any{"type": "string"},
					},
					"required": []string{"pattern", "why"},
				},
			},
		},
		"required": []string{"ai_request_id", "status"},
	}
}

func submitOrganizationToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"overview":       map[string]any{"type": "string", "description": "2-4 sentences, non-engineer language; omit if you cannot state the motivation honestly"},
			"version_number": map[string]any{"type": "integer", "description": "the version you organized, from get_review_files"},
			"chapters": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":   map[string]any{"type": "string"},
						"summary": map[string]any{"type": "string"},
						"files": map[string]any{
							"type":        "array",
							"description": "ordered by rank: scariest to review first",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"path":      map[string]any{"type": "string"},
									"risk":      map[string]any{"type": "string", "enum": []string{"high", "medium", "low", "mechanical"}},
									"rationale": map[string]any{"type": "string", "description": "one line: why it is here and what to verify"},
								},
								"required": []string{"path", "risk", "rationale"},
							},
						},
					},
					"required": []string{"title", "summary", "files"},
				},
			},
		},
		"required": []string{"chapters", "version_number"},
	}
}

func getReviewFilesToolSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
