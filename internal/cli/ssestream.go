package cli

import (
	"bufio"
	"context"
	"encoding/json"
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
func resolveReview(ctx context.Context, client *daemon.Client, session, cwd string) (reviewID string, port int, token string, err error) {
	for {
		resp, err := client.Resolve(session, cwd)
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

// ConsumeEvents streams a review's events (excluding Claude's own) to handle,
// persisting a per-consumer cursor so a restart resumes without re-delivering.
// It reconnects on transient drops and returns when handle signals stop or ctx
// is cancelled. The cursor advances only after handle returns, so a crash mid-
// delivery re-delivers rather than skips (at-least-once).
func ConsumeEvents(ctx context.Context, port int, token, reviewID, consumer string, handle EventHandler) error {
	cursorPath := paths.ConsumerCursorPath(reviewID, consumer)
	cursor := readCursor(cursorPath)
	base := fmt.Sprintf("http://127.0.0.1:%d/events?session=%s&t=%s&exclude_origin=claude",
		port, url.QueryEscape(reviewID), url.QueryEscape(token))
	for {
		if ctx.Err() != nil {
			return nil
		}
		stop, next := readStream(ctx, base, cursor, cursorPath, handle)
		cursor = next
		if stop || ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

func readStream(ctx context.Context, base string, cursor int64, cursorPath string, handle EventHandler) (stop bool, next int64) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return false, cursor
	}
	if cursor > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, cursor
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, cursor
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
				return false, cursor
			}
			cursor = id
			writeCursor(cursorPath, cursor)
			if s {
				return true, cursor
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
	return false, cursor
}

func eventType(data string) string {
	var e struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(data), &e)
	return e.Type
}

func readCursor(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func writeCursor(path string, cursor int64) {
	_ = os.WriteFile(path, []byte(strconv.FormatInt(cursor, 10)), 0o600)
}
