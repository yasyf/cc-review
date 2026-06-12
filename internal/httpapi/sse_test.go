package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/store"
)

type stubBackend struct {
	attached chan string
	detached chan string
	events   chan *store.Event
}

func (b *stubBackend) Subscribe(string) (<-chan struct{}, func()) {
	return make(chan struct{}), func() {}
}

func (b *stubBackend) AppendEvent(_ context.Context, e *store.Event) (int64, error) {
	b.events <- e
	return 0, nil
}

func (b *stubBackend) Attach(reviewID, consumer string, claudePID int) func() {
	key := reviewID + "/" + consumer + "/" + strconv.Itoa(claudePID)
	b.attached <- key
	return func() { b.detached <- key }
}

func (b *stubBackend) ClaudeConnected(string) bool { return false }

func newTestServer(t *testing.T) (*store.Store, *stubBackend, *httptest.Server) {
	st, _, backend, srv := newTestServerWithLedger(t)
	return st, backend, srv
}

func newTestServerWithLedger(t *testing.T) (*store.Store, *decisions.Log, *stubBackend, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ledger, err := decisions.Open(filepath.Join(dir, "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ledger.Close() })
	backend := &stubBackend{attached: make(chan string, 1), detached: make(chan string, 1), events: make(chan *store.Event, 4)}
	srv := httptest.NewServer(New(st, ledger, log.New(io.Discard, "", 0), backend).Handler())
	t.Cleanup(srv.Close)
	return st, ledger, backend, srv
}

func TestEventsRegistersNamedConsumer(t *testing.T) {
	st, backend, srv := newTestServer(t)
	review, err := st.CreateReview(context.Background(), "s1", 100, "/repo", "main", "base0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?session="+review.ID+"&consumer=channel&claude_pid=4242", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Wait for the liveness comment so the handler is fully set up.
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "connected") {
			break
		}
	}
	want := review.ID + "/channel/4242"
	select {
	case got := <-backend.attached:
		if got != want {
			t.Fatalf("attached %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer was never attached")
	}

	cancel()
	select {
	case got := <-backend.detached:
		if got != want {
			t.Fatalf("detached %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer was never detached on disconnect")
	}
}

func TestEventsSlugRefAttachesUnderCanonicalID(t *testing.T) {
	st, backend, srv := newTestServer(t)
	review, err := st.CreateReview(context.Background(), "s1", 100, "/repo", "feat/x", "base0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?session="+review.Slug+"&consumer=channel", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "connected") {
			break
		}
	}
	// The Bus and events table key on the full id, so the slug must be
	// translated before any subscription. No claude_pid param is a legitimate
	// pid-less consumer, registered under pid 0.
	want := review.ID + "/channel/0"
	select {
	case got := <-backend.attached:
		if got != want {
			t.Fatalf("attached %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer was never attached")
	}
}

func TestEventsChannelChangedStampsLatestVersion(t *testing.T) {
	st, backend, srv := newTestServer(t)
	setup := context.Background()
	review, err := st.CreateReview(setup, "s1", 100, "/repo", "main", "base0")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := st.CreateVersion(setup, review.ID, "main", "HEAD", "/p", "[]"); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?session="+review.ID+"&consumer=channel&claude_pid=4242", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	select {
	case ev := <-backend.events:
		if ev.Type != store.EventChannelChanged || ev.Origin != store.OriginSystem || ev.VersionNumber != 2 {
			t.Fatalf("event type=%s origin=%s version=%d, want system channel.changed on version 2",
				ev.Type, ev.Origin, ev.VersionNumber)
		}
		var payload struct {
			Connected     bool `json:"connected"`
			VersionNumber int  `json:"version_number"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Connected || payload.VersionNumber != 2 {
			t.Fatalf("payload = %+v, want connected:true on version 2", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel.changed was never emitted on attach")
	}
}

func TestEventsUnknownReviewIs404(t *testing.T) {
	_, _, srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/events?session=nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestEventsBadClaudePIDIs400(t *testing.T) {
	st, _, srv := newTestServer(t)
	review, err := st.CreateReview(context.Background(), "s1", 100, "/repo", "main", "base0")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/events?session=" + review.ID + "&consumer=channel&claude_pid=garbage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEventsBrowserConsumerNotRegistered(t *testing.T) {
	st, backend, srv := newTestServer(t)
	review, err := st.CreateReview(context.Background(), "s1", 100, "/repo", "main", "base0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?session="+review.Slug, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "connected") {
			break
		}
	}
	select {
	case got := <-backend.attached:
		t.Fatalf("browser connection (no consumer param) was registered as %q", got)
	default:
	}
}
