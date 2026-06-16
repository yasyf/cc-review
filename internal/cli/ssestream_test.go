package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamURLClaudePID(t *testing.T) {
	for _, tc := range []struct {
		name string
		pid  int
		want string
	}{
		{"non-zero pid rides the URL", 4242, "&claude_pid=4242"},
		{"zero pid stays absent", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := streamURL(StreamSource{Port: 1, ReviewID: "r", Consumer: "watch", ClaudePID: tc.pid})
			if tc.want != "" && !strings.Contains(u, tc.want) {
				t.Fatalf("url %q missing %q", u, tc.want)
			}
			if tc.want == "" && strings.Contains(u, "claude_pid") {
				t.Fatalf("url %q must not carry claude_pid for pid 0", u)
			}
		})
	}
}

func ssePort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// TestConsumeEventsSendsConsumerParamAndRefreshes proves the two stream-survival
// properties: the consumer name rides the SSE URL, and after the first server
// dies the Refresh hook redirects the stream to the replacement daemon.
func TestConsumeEventsSendsConsumerParamAndRefreshes(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // ConsumeEvents EnsureReviewDir's under ~/.cc-review
	var sawConsumerA, sawConsumerB atomic.Value

	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawConsumerA.Store(r.URL.Query().Get("consumer"))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\ndata: {\"type\":\"comment.created\"}\n\n")
		// Returning closes the stream: the "old daemon" dying mid-session.
	}))
	t.Cleanup(a.Close)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawConsumerB.Store(r.URL.Query().Get("consumer"))
		if got := r.Header.Get("Last-Event-ID"); got != "1" {
			t.Errorf("Last-Event-ID = %q, want 1 (cursor must survive the refresh)", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 2\ndata: {\"type\":\"submit\"}\n\n")
	}))
	t.Cleanup(b.Close)

	src := StreamSource{
		Port: ssePort(t, a), ReviewID: "stream-test", Consumer: "watch",
		Refresh: func(context.Context) (int, error) {
			return ssePort(t, b), nil
		},
	}
	var got []string
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := ConsumeEvents(ctx, src, func(_ int64, data string) (bool, error) {
		got = append(got, eventType(data))
		return eventType(data) == "submit", nil
	})
	if err != nil {
		t.Fatalf("ConsumeEvents: %v", err)
	}
	if len(got) != 2 || got[0] != "comment.created" || got[1] != "submit" {
		t.Fatalf("delivered %v, want [comment.created submit]", got)
	}
	if sawConsumerA.Load() != "watch" || sawConsumerB.Load() != "watch" {
		t.Fatalf("consumer param missing: a=%v b=%v", sawConsumerA.Load(), sawConsumerB.Load())
	}
}

// TestConsumeEventsExitsWhenClaudeGone proves a pid-bound consumer whose window
// is gone exits before opening the stream — so a leaked watcher can't keep
// draining events (and advancing the shared cursor) after its Claude died.
func TestConsumeEventsExitsWhenClaudeGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 1\ndata: {\"type\":\"comment.created\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	// os.Getpid() is alive but its argv is the test binary, not claude, so
	// LiveClaude is false — the consumer must treat the window as gone.
	src := StreamSource{Port: ssePort(t, srv), ReviewID: "gone-test", Consumer: "watch", ClaudePID: os.Getpid()}
	delivered := 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := ConsumeEvents(ctx, src, func(_ int64, _ string) (bool, error) {
		delivered++
		return false, nil
	})
	if err != nil {
		t.Fatalf("ConsumeEvents: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("delivered %d events, want 0 — a dead-window consumer must not consume", delivered)
	}
	if hits.Load() != 0 {
		t.Fatalf("server hit %d times, want 0 — must exit before connecting", hits.Load())
	}
}
