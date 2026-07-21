package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/yasyf/cc-interact/channel"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/store"
)

// channelNotifyMethod is the JSON-RPC method each subject event is pushed under
// on the Claude channel.
const channelNotifyMethod = "notifications/claude/channel"

// channelInstructions is folded into the agent's system prompt at the channel's
// MCP initialize, so every --channels session (even one that never ran
// cc-review:start) knows what channel traffic to expect and that silence is
// normal.
var channelInstructions = channel.Instructions(channel.InstructionsSpec{
	Desc:    "the cc-review code-review channel",
	Traffic: "Review activity reaches you",
	Source:  "cc-review",
	Guide: `A channel.probe tag may arrive right after this session runs cc-review start — it is a delivery check, not a request: run "${CLAUDE_PLUGIN_ROOT}/bin/cc-review" channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD", and reply nothing.

Real review input arrives as other event types such as comment.created, comment.updated, ai.request.created, and submit. The cc-review:start skill governs how to handle those.`,
	SilentOutside: "a /cc-review:start run",
})

// channelTools advertises cc-review's review tools to the agent's MCP channel.
// The handlers round-trip to the daemon via ReviewClient because the channel
// server is a separate stdio process and cannot touch the store directly.
func channelTools(ctx context.Context, session, scope string) ([]channel.Tool, string, string, error) {
	rc, err := daemon.NewReviewClient(ctx)
	if err != nil {
		return nil, "", "", err
	}
	go func() {
		<-ctx.Done()
		_ = rc.Close()
	}()
	tools := []channel.Tool{
		{
			Name:        "reply",
			Description: "Post a question, structured ask, or clarification under a cc-review comment, or answer one.",
			InputSchema: replyToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage, _ func(string)) (string, bool) {
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
			Handler: func(ctx context.Context, args json.RawMessage, _ func(string)) (string, bool) {
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
			Name:        "set_file_states_by_risk",
			Description: "Flip every file the current organization already tags with one of the given risk levels (high|medium|low|mechanical) to reviewed/hidden in one batch — the server resolves the path set from the organization, so you never enumerate or re-read files. This is the shortcut for requests like \"mark all mechanical changes as viewed\". ai_request_id ties the batch to an AI request as one undoable unit. Returns the affected paths.",
			InputSchema: setFileStatesByRiskToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage, _ func(string)) (string, bool) {
				var in struct {
					Risk        []string `json:"risk"`
					Reviewed    *bool    `json:"reviewed"`
					Hidden      *bool    `json:"hidden"`
					Reason      string   `json:"reason"`
					AIRequestID string   `json:"ai_request_id"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				id, err := parseAIRequestID(in.AIRequestID, false)
				if err != nil {
					return err.Error(), true
				}
				paths, err := rc.FileStatesByRisk(ctx, session, scope, in.Risk, in.Reviewed, in.Hidden, in.Reason, id)
				if err != nil {
					return err.Error(), true
				}
				if len(paths) == 0 {
					return "no files matched those risk levels", false
				}
				return fmt.Sprintf("flipped %d files: %s", len(paths), strings.Join(paths, ", ")), false
			},
		},
		{
			Name:        "update_ai_request",
			Description: "Move an AI request through its lifecycle: working when you start; done or failed when you finish (with a summary and any unmatched prompt parts); or awaiting_input when the request's INTENT is ambiguous (not merely large) and you must ask the reviewer one clarifying question. awaiting_input ends your run; the reviewer answers and the request is redispatched to you with status answered, carrying the original prompt plus your question and their answer.",
			InputSchema: updateAIRequestToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage, _ func(string)) (string, bool) {
				var in struct {
					AIRequestID string            `json:"ai_request_id"`
					Status      string            `json:"status"`
					Summary     string            `json:"summary"`
					Phase       string            `json:"phase"`
					Unmatched   []store.Unmatched `json:"unmatched"`
					Question    string            `json:"question"`
					Ask         *store.Ask        `json:"ask"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				id, err := parseAIRequestID(in.AIRequestID, true)
				if err != nil {
					return err.Error(), true
				}
				if err := rc.UpdateAIRequest(ctx, session, scope, id, daemon.UpdateAIRequestInput{
					Status: in.Status, Summary: in.Summary, Phase: in.Phase, Unmatched: in.Unmatched, Question: in.Question, Ask: in.Ask,
				}); err != nil {
					return err.Error(), true
				}
				return "ok", false
			},
		},
		{
			Name:        "submit_organization",
			Description: "Submit the review's chapter organization: every changed file in exactly one chapter, each rated by the risk of skimming it. Chapter order is narrative; file order within a chapter is rank, scariest first. On resubmit keep every entry the new information doesn't touch byte-identical — the UI animates only what moved. Pass partial:true to stream an in-progress organization as you classify (files not yet placed are allowed); the reviewer watches chapters fill in. Your final submit must omit partial so full coverage is enforced. A stale version_number is rejected with the current one.",
			InputSchema: submitOrganizationToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage, _ func(string)) (string, bool) {
				var in struct {
					Overview      *string         `json:"overview"`
					VersionNumber int             `json:"version_number"`
					Chapters      json.RawMessage `json:"chapters"`
					Partial       bool            `json:"partial"`
				}
				decoder := json.NewDecoder(bytes.NewReader(args))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				if err := decoder.Decode(&struct{}{}); err != io.EOF {
					return "bad tool arguments: trailing JSON", true
				}
				raw, err := json.Marshal(struct {
					Overview *string         `json:"overview"`
					Chapters json.RawMessage `json:"chapters"`
				}{Overview: in.Overview, Chapters: in.Chapters})
				if err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				var org store.Organization
				if err := json.Unmarshal(raw, &org); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				if err := rc.SubmitOrganization(ctx, session, scope, org, in.VersionNumber, in.Partial); err != nil {
					return err.Error(), true
				}
				return "ok", false
			},
		},
		{
			Name:        "annotate",
			Description: "Mark specific line ranges of the diff for the reviewer, on an AI-bar request (e.g. \"highlight the lines actually changed, not just copied from the old file\"). Each item is kind \"highlight\" — a non-blocking colored line-range mark with an optional label — or kind \"comment\" — a Claude-authored comment thread the reviewer can reply to. Annotations never gate the reviewer's submit. Call it per file as you work to stream marks in; ai_request_id ties highlights to the request for undo.",
			InputSchema: annotateToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage, _ func(string)) (string, bool) {
				var in struct {
					Items       []daemon.AnnotateInput `json:"items"`
					AIRequestID string                 `json:"ai_request_id"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "bad tool arguments: " + err.Error(), true
				}
				id, err := parseAIRequestID(in.AIRequestID, false)
				if err != nil {
					return err.Error(), true
				}
				if err := rc.Annotate(ctx, session, scope, in.Items, id); err != nil {
					return err.Error(), true
				}
				return fmt.Sprintf("added %d annotations", len(in.Items)), false
			},
		},
		{
			Name:        "get_review_files",
			Description: "List the open cc-review's current version_number and patch_path (the on-disk unified diff), plus review_files_path (the full file list as JSONL — one {path,status,reviewed,hidden} per line) and organization_path (the latest organization as JSON: overview + chapters with basis_version, per-file delta changed/moved/removed and new_paths). Read those paths from disk. A small file set — or a status/reviewed/hidden-filtered subset — is also inlined as files with match_count, so the result never overflows on a large review.",
			InputSchema: getReviewFilesToolSchema(),
			Handler: func(ctx context.Context, args json.RawMessage, _ func(string)) (string, bool) {
				var in struct {
					Status   string `json:"status"`
					Reviewed *bool  `json:"reviewed"`
					Hidden   *bool  `json:"hidden"`
				}
				if len(args) > 0 {
					if err := json.Unmarshal(args, &in); err != nil {
						return "bad tool arguments: " + err.Error(), true
					}
				}
				raw, err := rc.ReviewFiles(ctx, session, scope, daemon.ReviewFilesFilter{Status: in.Status, Reviewed: in.Reviewed, Hidden: in.Hidden})
				if err != nil {
					return err.Error(), true
				}
				return string(raw), false
			},
		},
	}
	return tools, channelNotifyMethod, channelInstructions, nil
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

func setFileStatesByRiskToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"risk": map[string]any{
				"type":        "array",
				"description": "risk levels to flip: any of high, medium, low, mechanical",
				"items":       map[string]any{"type": "string", "enum": []string{"high", "medium", "low", "mechanical"}},
			},
			"reviewed":      map[string]any{"type": "boolean", "description": "reviewed state to set on every matched file"},
			"hidden":        map[string]any{"type": "boolean", "description": "hidden state to set on every matched file"},
			"reason":        map[string]any{"type": "string", "description": "one line recorded for every matched file"},
			"ai_request_id": map[string]any{"type": "string", "description": "id of the AI request these changes belong to"},
		},
		"required": []string{"risk"},
	}
}

func updateAIRequestToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ai_request_id": map[string]any{"type": "string"},
			"status":        map[string]any{"type": "string", "enum": []string{"working", "done", "failed", "awaiting_input"}},
			"summary":       map[string]any{"type": "string", "description": "one sentence: what you did and why (done/failed)"},
			"phase":         map[string]any{"type": "string", "description": "short present-tense progress label shown live while working, e.g. \"reading 8 files…\""},
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
			"question": map[string]any{"type": "string", "description": "required for status=awaiting_input: the one clarifying question to ask the reviewer"},
			"ask": map[string]any{
				"type":        "object",
				"description": "optional structured options for the question (status=awaiting_input), mirroring AskUserQuestion",
				"properties": map[string]any{
					"header":      map[string]any{"type": "string", "description": "short chip, e.g. Scope"},
					"multiSelect": map[string]any{"type": "boolean"},
					"options": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label":       map[string]any{"type": "string"},
								"description": map[string]any{"type": "string"},
								"preview":     map[string]any{"type": "string"},
							},
							"required": []string{"label"},
						},
					},
				},
				"required": []string{"options"},
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
									"focus":     map[string]any{"type": "string", "description": "what to focus on in this file and why it carries this risk"},
									"lines": map[string]any{
										"type":        "array",
										"description": "importance ranges over NEW-side added lines (1-based); use an empty array for files reviewed normally",
										"items": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"start": map[string]any{"type": "integer", "description": "first added line (NEW-side, 1-based), inclusive"},
												"end":   map[string]any{"type": "integer", "description": "last added line, inclusive"},
												"level": map[string]any{"type": "string", "enum": []string{"focus", "mechanical"}},
												"note":  map[string]any{"type": "string", "description": "focus hint shown on hover; give it for focus ranges"},
											},
											"required": []string{"start", "end", "level", "note"},
										},
									},
								},
								"required": []string{"path", "risk", "rationale", "focus", "lines"},
							},
						},
					},
					"required": []string{"title", "summary", "files"},
				},
			},
			"partial": map[string]any{"type": "boolean", "description": "true while streaming an in-progress organization; omit on the final, complete submit"},
		},
		"required": []string{"chapters", "version_number"},
	}
}

func annotateToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":       map[string]any{"type": "string", "enum": []string{"highlight", "comment"}, "description": "highlight = informational line-range mark; comment = a Claude-authored reply thread"},
						"file_path":  map[string]any{"type": "string"},
						"side":       map[string]any{"type": "string", "enum": []string{"additions", "deletions"}, "description": "which side of the diff the lines are on"},
						"start_line": map[string]any{"type": "integer", "description": "1-based first line on that side"},
						"end_line":   map[string]any{"type": "integer", "description": "1-based last line on that side (inclusive)"},
						"body":       map[string]any{"type": "string", "description": "the highlight's label (optional) or the comment's text (required for kind=comment)"},
					},
					"required": []string{"kind", "file_path", "side", "start_line", "end_line"},
				},
			},
			"ai_request_id": map[string]any{"type": "string", "description": "id of the AI request these annotations belong to"},
		},
		"required": []string{"items"},
	}
}

func getReviewFilesToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":   map[string]any{"type": "string", "description": "narrow the inline files subset to this git status letter (M, A, D, R)"},
			"reviewed": map[string]any{"type": "boolean", "description": "narrow the inline files subset to this reviewed state"},
			"hidden":   map[string]any{"type": "boolean", "description": "narrow the inline files subset to this hidden flag"},
		},
	}
}
