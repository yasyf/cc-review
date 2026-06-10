package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/store"
)

type stubBackend struct {
	attached chan string
	detached chan string
}

func (b *stubBackend) Subscribe(string) (<-chan struct{}, func()) {
	return make(chan struct{}), func() {}
}

func (b *stubBackend) AppendEvent(_ context.Context, _ *store.Event) (int64, error) {
	return 0, nil
}

func (b *stubBackend) Attach(reviewID, consumer string) func() {
	key := reviewID + "/" + consumer
	b.attached <- key
	return func() { b.detached <- key }
}

func newTestServer(t *testing.T) (*store.Store, *stubBackend, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	backend := &stubBackend{attached: make(chan string, 1), detached: make(chan string, 1)}
	srv := httptest.NewServer(New(st, backend).Handler())
	t.Cleanup(srv.Close)
	return st, backend, srv
}

func TestEventsRegistersNamedConsumer(t *testing.T) {
	st, backend, srv := newTestServer(t)
	review, err := st.CreateReview(context.Background(), "s1", "/repo", "main")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?session="+review.ID+"&consumer=channel", nil)
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
	want := review.ID + "/channel"
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
	review, err := st.CreateReview(context.Background(), "s1", "/repo", "feat/x")
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
	// translated before any subscription.
	want := review.ID + "/channel"
	select {
	case got := <-backend.attached:
		if got != want {
			t.Fatalf("attached %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer was never attached")
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

func TestEventsBrowserConsumerNotRegistered(t *testing.T) {
	st, backend, srv := newTestServer(t)
	review, err := st.CreateReview(context.Background(), "s1", "/repo", "main")
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
