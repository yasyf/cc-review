package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
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

func (c *channel) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out.Write(b)
	c.out.Write([]byte("\n"))
}

func (c *channel) reply(id json.RawMessage, result any) {
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *channel) notify(method string, params any) {
	c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
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
				"capabilities":    map[string]any{"tools": map[string]any{}, "claude/channel": map[string]any{}},
				"serverInfo":      map[string]any{"name": "cc-review", "version": version.String()},
			})
		case "tools/list":
			ch.reply(msg.ID, map[string]any{"tools": []any{replyToolSchema()}})
		case "tools/call":
			ch.reply(msg.ID, handleToolCall(msg.Params))
		default:
			ch.reply(msg.ID, map[string]any{})
		}
	}
	return sc.Err()
}

// streamToChannel waits for the daemon + review, then pushes every human event
// as a claude/channel notification for the lifetime of the session.
func streamToChannel(ctx context.Context, ch *channel, session, cwd string) {
	client := daemon.NewClient()
	reviewID, port, token := waitForReview(ctx, client, session, cwd)
	if reviewID == "" {
		return
	}
	_ = ConsumeEvents(ctx, port, token, reviewID, "channel", func(_ int64, data string) (bool, error) {
		ch.notify("notifications/claude/channel", map[string]any{
			"content": data,
			"meta":    map[string]any{"source": "cc-review", "type": eventType(data), "review_id": reviewID},
		})
		return false, nil // a channel runs for the whole session
	})
}

func waitForReview(ctx context.Context, client *daemon.Client, session, cwd string) (reviewID string, port int, token string) {
	for {
		if ctx.Err() != nil {
			return "", 0, ""
		}
		if client.Available() {
			if resp, err := client.Resolve(session, cwd); err == nil && resp.ReviewID != "" {
				return resp.ReviewID, resp.HTTPPort, resp.Token
			}
		}
		select {
		case <-ctx.Done():
			return "", 0, ""
		case <-time.After(time.Second):
		}
	}
}

func replyToolSchema() map[string]any {
	return map[string]any{
		"name":        "reply",
		"description": "Post a question, option set, or clarification under a cc-review comment, or answer a question.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"comment_id": map[string]any{"type": "integer", "description": "comment id to reply under"},
				"kind":       map[string]any{"type": "string", "enum": []string{"question", "option", "clarification"}},
				"body":       map[string]any{"type": "string"},
				"options":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"answer":     map[string]any{"type": "string"},
				"answer_to":  map[string]any{"type": "integer", "description": "reply id of a question being answered"},
			},
		},
	}
}

func handleToolCall(params json.RawMessage) map[string]any {
	var call struct {
		Name      string            `json:"name"`
		Arguments daemon.ReplyInput `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return toolError("bad tool arguments: " + err.Error())
	}
	if call.Name != "reply" {
		return toolError("unknown tool: " + call.Name)
	}
	resp, err := daemon.NewClient().Reply([]daemon.ReplyInput{call.Arguments})
	if err != nil {
		return toolError(err.Error())
	}
	if !resp.OK {
		return toolError(resp.Error)
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
}

func toolError(msg string) map[string]any {
	return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": msg}}}
}
