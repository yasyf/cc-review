package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/paths"
)

// reconnectDelay is how long a stream consumer waits before reconnecting after
// the SSE connection drops.
const reconnectDelay = 2 * time.Second

// EventHandler is invoked once per delivered event with its seq and raw JSON.
// Returning stop=true ends consumption (e.g. on the terminal submit event).
type EventHandler func(seq int64, data string) (stop bool, err error)

// resolveReview polls the daemon until a review exists for the session+cwd,
// returning its id and the HTTP handshake. A stream consumer may start before
// `start` has created the review (e.g. an MCP channel loaded at session start).
// consumer names the caller so the daemon can track its presence.
func resolveReview(ctx context.Context, client *daemon.Client, session, cwd, consumer string) (reviewID string, port int, token string, err error) {
	for {
		resp, err := client.Resolve(session, cwd, consumer)
		if err != nil {
			return "", 0, "", err
		}
		if resp.ReviewID != "" {
			return resp.ReviewID, resp.HTTPPort, resp.Token, nil
		}
		select {
		case <-ctx.Done():
			return "", 0, "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// StreamSource identifies one SSE consumption: where to connect, as whom, and
// how to refresh the handshake — a version-skew eviction restarts the daemon
// mid-session, changing the port and token the consumer captured.
type StreamSource struct {
	Port     int
	Token    string
	ReviewID string
	Consumer string
	Refresh  func(ctx context.Context) (port int, token string, err error)
}

// ConsumeEvents streams a review's events (excluding Claude's own) to handle,
// persisting a per-consumer cursor so a restart resumes without re-delivering.
// It reconnects on transient drops — refreshing the handshake first, so the
// stream survives a daemon upgrade — and returns when handle signals stop or
// ctx is cancelled. The cursor advances only after handle returns, so a crash
// mid-delivery re-delivers rather than skips (at-least-once).
func ConsumeEvents(ctx context.Context, src StreamSource, handle EventHandler) error {
	cursorPath := paths.ConsumerCursorPath(src.ReviewID, src.Consumer)
	cursor, err := readCursor(cursorPath)
	if err != nil {
		return err // a corrupt cursor would silently replay the whole backlog; fail loud
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		stop, next, fatal := readStream(ctx, streamURL(src), cursor, cursorPath, handle)
		cursor = next
		if stop || ctx.Err() != nil {
			return nil
		}
		refreshed := false
		if src.Refresh != nil {
			if port, token, err := src.Refresh(ctx); err == nil && port != 0 {
				refreshed = port != src.Port || token != src.Token
				src.Port, src.Token = port, token
			}
		}
		if fatal != nil && !refreshed {
			return fatal // a 4xx against the *current* handshake is permanent
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

func streamURL(src StreamSource) string {
	return fmt.Sprintf("http://127.0.0.1:%d/events?session=%s&t=%s&exclude_origin=claude&consumer=%s",
		src.Port, url.QueryEscape(src.ReviewID), url.QueryEscape(src.Token), url.QueryEscape(src.Consumer))
}

// refreshHandshake returns a StreamSource.Refresh that re-resolves the daemon's
// current port+token, so a stream survives the daemon being replaced underneath it.
func refreshHandshake(client *daemon.Client, session, cwd, consumer string) func(context.Context) (int, string, error) {
	return func(context.Context) (int, string, error) {
		resp, err := client.Resolve(session, cwd, consumer)
		if err != nil {
			return 0, "", err
		}
		return resp.HTTPPort, resp.Token, nil
	}
}

// readStream consumes one SSE connection. A nil fatal means a transient drop
// (reconnect); a non-nil fatal is a permanent failure (bad token, unknown review,
// or an event frame larger than the buffer) the caller should surface and stop on.
func readStream(ctx context.Context, base string, cursor int64, cursorPath string, handle EventHandler) (stop bool, next int64, fatal error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return false, cursor, err
	}
	if cursor > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, cursor, nil // transient: connection refused / reset → reconnect
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return false, cursor, fmt.Errorf("events endpoint returned %d (stale token or unknown review); stopping", resp.StatusCode)
		}
		return false, cursor, nil // 5xx: reconnect
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var data string
	id := cursor
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data == "" {
				continue
			}
			s, herr := handle(id, data)
			if herr != nil {
				return false, cursor, nil // delivery failed: don't advance; reconnect re-delivers
			}
			cursor = id
			writeCursor(cursorPath, cursor)
			if s {
				return true, cursor, nil
			}
			data = ""
		case strings.HasPrefix(line, ":"):
			// comment / keepalive
		case strings.HasPrefix(line, "id:"):
			if n, e := strconv.ParseInt(strings.TrimSpace(line[len("id:"):]), 10, 64); e == nil {
				id = n
			}
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(line[len("data:"):])
		}
	}
	if err := sc.Err(); errors.Is(err, bufio.ErrTooLong) {
		return false, cursor, fmt.Errorf("event frame exceeded the %d-byte buffer; stopping: %w", 4*1024*1024, err)
	}
	return false, cursor, nil
}

func eventType(data string) string {
	var e struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(data), &e)
	return e.Type
}

// readCursor returns the persisted cursor, 0 when absent, and an error on a
// corrupt file (so a torn write replays the backlog loudly, not silently).
func readCursor(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read cursor %s: %w", path, err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("corrupt cursor %s: %w", path, err)
	}
	return n, nil
}

// writeCursor persists the cursor atomically (temp + rename) so a crash can't
// leave a torn value that resets the consumer to 0.
func writeCursor(path string, cursor int64) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(cursor, 10)), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
