package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/procs"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/version"
)

// mcpProtocolVersion is the MCP version advertised when the client omits one.
const mcpProtocolVersion = "2025-06-18"

// newMCPChannelCmd is the hidden, opt-in Claude "channel" server. Loaded at
// session start under `--channels`, it declares the claude/channel capability,
// pushes each human review event as a channel notification (so Claude reacts
// without polling), and exposes a two-way `reply` tool. The default Monitor path
// (cc-review watch) needs none of this — it is an enhancement.
func newMCPChannelCmd() *cobra.Command {
	var (
		session string
		cwd     string
	)
	cmd := &cobra.Command{
		Use:    "mcp-channel",
		Hidden: true,
		Short:  "Run the opt-in Claude channel MCP server (stdio)",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if session == "" {
				session = os.Getenv("CLAUDE_CODE_SESSION_ID")
			}
			return runMCPChannel(cmd.Context(), session, mustCwd(cwd), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id (defaults to $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type channel struct {
	mu  sync.Mutex
	out io.Writer
}

func (c *channel) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.out.Write(b); err != nil {
		return err
	}
	_, err = c.out.Write([]byte("\n"))
	return err
}

func (c *channel) reply(id json.RawMessage, result any) {
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *channel) replyError(id json.RawMessage, code int, message string) {
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (c *channel) notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func runMCPChannel(ctx context.Context, session, cwd string, in io.Reader, out io.Writer) error {
	ch := &channel{out: out}
	go streamToChannel(ctx, ch, session, cwd)

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if len(msg.ID) == 0 {
			continue // notification from the client; nothing to answer
		}
		switch msg.Method {
		case "initialize":
			ch.reply(msg.ID, map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}, "experimental": map[string]any{"claude/channel": map[string]any{}}},
				"serverInfo":      map[string]any{"name": "cc-review", "version": version.String()},
			})
		case "tools/list":
			ch.reply(msg.ID, map[string]any{"tools": []any{
				replyToolSchema(), setFileStatesToolSchema(), updateAIRequestToolSchema(),
				submitOrganizationToolSchema(), getReviewFilesToolSchema(),
			}})
		case "tools/call":
			ch.reply(msg.ID, handleToolCall(msg.Params, session, cwd))
		case "ping":
			ch.reply(msg.ID, map[string]any{})
		default:
			ch.replyError(msg.ID, -32601, "method not found: "+msg.Method)
		}
	}
	return sc.Err()
}

// streamToChannel waits for the daemon + review, then pushes every human event
// as a claude/channel notification for the lifetime of the session. The window
// pid is resolved once at boot — the channel server lives as long as the
// window — and keys the stream even when $CLAUDE_CODE_SESSION_ID is stale or
// unset.
func streamToChannel(ctx context.Context, ch *channel, session, cwd string) {
	client := daemon.NewClient()
	claudePID := procs.ClaudePID()
	reviewID, port := waitForReview(ctx, client, session, cwd)
	if reviewID == "" {
		return
	}
	src := StreamSource{
		Port: port, ReviewID: reviewID, Consumer: "channel", ClaudePID: claudePID,
		Refresh: refreshHandshake(client, session, cwd, "channel"),
	}
	_ = ConsumeEvents(ctx, src, func(_ int64, data string) (bool, error) {
		// A failed push must propagate so the cursor doesn't advance past an
		// undelivered event; a channel otherwise runs for the whole session.
		err := ch.notify("notifications/claude/channel", map[string]any{
			"content": data,
			"meta":    map[string]any{"type": eventType(data), "review_id": reviewID},
		})
		return false, err
	})
}

func waitForReview(ctx context.Context, client *daemon.Client, session, cwd string) (reviewID string, port int) {
	for {
		if ctx.Err() != nil {
			return "", 0
		}
		if client.Available() {
			if resp, err := client.Resolve(session, cwd, "channel"); err == nil && resp.ReviewID != "" {
				return resp.ReviewID, resp.HTTPPort
			}
		}
		select {
		case <-ctx.Done():
			return "", 0
		case <-time.After(time.Second):
		}
	}
}

func replyToolSchema() map[string]any {
	return map[string]any{
		"name":        "reply",
		"description": "Post a question, structured ask, or clarification under a cc-review comment, or answer one.",
		"inputSchema": map[string]any{
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
		},
	}
}

func setFileStatesToolSchema() map[string]any {
	return map[string]any{
		"name":        "set_file_states",
		"description": "Batch-set per-file review state (reviewed/hidden) on the open cc-review. ai_request_id ties the batch to an AI request as one undoable unit.",
		"inputSchema": map[string]any{
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
		},
	}
}

func updateAIRequestToolSchema() map[string]any {
	return map[string]any{
		"name":        "update_ai_request",
		"description": "Move an AI request through its lifecycle: working when you start, done or failed when you finish (with a summary and any unmatched prompt parts).",
		"inputSchema": map[string]any{
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
		},
	}
}

func submitOrganizationToolSchema() map[string]any {
	return map[string]any{
		"name":        "submit_organization",
		"description": "Submit the review's chapter organization: every changed file in exactly one chapter, each rated by the risk of skimming it. A stale version_number is rejected with the current one.",
		"inputSchema": map[string]any{
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
								"type": "array",
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
			"required": []string{"chapters"},
		},
	}
}

func getReviewFilesToolSchema() map[string]any {
	return map[string]any{
		"name":        "get_review_files",
		"description": "List the open cc-review's current version number and files with status and review state — the server truth bulk operations must act on. Includes the latest organization (overview + chapters) with basis_version, per-file delta (changed/moved/removed; absent = unchanged), and new_paths.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func handleToolCall(params json.RawMessage, session, cwd string) map[string]any {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return toolError("bad tool arguments: " + err.Error())
	}
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage("{}")
	}
	client := daemon.NewClient()
	switch call.Name {
	case "reply":
		var in daemon.ReplyInput
		if err := json.Unmarshal(call.Arguments, &in); err != nil {
			return toolError("bad tool arguments: " + err.Error())
		}
		return toolResult(client.Reply([]daemon.ReplyInput{in}))
	case "set_file_states":
		var in struct {
			Files       []daemon.FileStateInput `json:"files"`
			AIRequestID string                  `json:"ai_request_id"`
		}
		if err := json.Unmarshal(call.Arguments, &in); err != nil {
			return toolError("bad tool arguments: " + err.Error())
		}
		id, err := parseAIRequestID(in.AIRequestID, false)
		if err != nil {
			return toolError(err.Error())
		}
		return toolResult(client.FileStates(session, cwd, in.Files, id))
	case "update_ai_request":
		var in struct {
			AIRequestID string            `json:"ai_request_id"`
			Status      string            `json:"status"`
			Summary     string            `json:"summary"`
			Unmatched   []store.Unmatched `json:"unmatched"`
		}
		if err := json.Unmarshal(call.Arguments, &in); err != nil {
			return toolError("bad tool arguments: " + err.Error())
		}
		id, err := parseAIRequestID(in.AIRequestID, true)
		if err != nil {
			return toolError(err.Error())
		}
		return toolResult(client.UpdateAIRequest(session, cwd, id, in.Status, in.Summary, in.Unmatched))
	case "submit_organization":
		var in struct {
			Overview      *string         `json:"overview"`
			VersionNumber int             `json:"version_number"`
			Chapters      []store.Chapter `json:"chapters"`
		}
		if err := json.Unmarshal(call.Arguments, &in); err != nil {
			return toolError("bad tool arguments: " + err.Error())
		}
		org := store.Organization{Overview: in.Overview, Chapters: in.Chapters}
		return toolResult(client.SubmitOrganization(session, cwd, org, in.VersionNumber))
	case "get_review_files":
		resp, err := client.ReviewFiles(session, cwd)
		if err != nil {
			return toolError(err.Error())
		}
		if !resp.OK {
			return toolError(resp.Error)
		}
		return toolOK(string(resp.ReviewFiles))
	default:
		return toolError("unknown tool: " + call.Name)
	}
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

func toolResult(resp *daemon.Response, err error) map[string]any {
	if err != nil {
		return toolError(err.Error())
	}
	if !resp.OK {
		return toolError(resp.Error)
	}
	return toolOK("ok")
}

func toolOK(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}

func toolError(msg string) map[string]any {
	return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": msg}}}
}
