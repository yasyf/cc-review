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

func TestEventsRegistersNamedConsumer(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	backend := &stubBackend{attached: make(chan string, 1), detached: make(chan string, 1)}
	srv := httptest.NewServer(New(st, backend, "tok").Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?t=tok&session=r1&consumer=channel", nil)
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
	select {
	case got := <-backend.attached:
		if got != "r1/channel" {
			t.Fatalf("attached %q, want r1/channel", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer was never attached")
	}

	cancel()
	select {
	case got := <-backend.detached:
		if got != "r1/channel" {
			t.Fatalf("detached %q, want r1/channel", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer was never detached on disconnect")
	}
}

func TestEventsBrowserConsumerNotRegistered(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	backend := &stubBackend{attached: make(chan string, 1), detached: make(chan string, 1)}
	srv := httptest.NewServer(New(st, backend, "tok").Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?t=tok&session=r1", nil)
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
