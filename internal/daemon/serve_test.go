package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/testhome"
)

func TestServeMountsRESTWithActivatedDB(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cc-review-serve-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	testhome.Pin(t, home)
	t.Setenv("CC_DECISIONS_DB", filepath.Join(home, "decisions.db"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, 0) }()

	client := &http.Client{Timeout: 250 * time.Millisecond}
	httpInfoPath := filepath.Join(home, ".cc-review", "v1", "http.json")
	deadline := time.Now().Add(10 * time.Second)
	var resp *http.Response
	var lastErr error
	for resp == nil {
		data, readErr := os.ReadFile(httpInfoPath) // #nosec G304 -- path under the test's own temp HOME
		if readErr != nil {
			lastErr = readErr
		} else {
			var info struct {
				Port int `json:"port"`
			}
			if decodeErr := json.Unmarshal(data, &info); decodeErr != nil {
				lastErr = decodeErr
			} else if info.Port == 0 {
				lastErr = fmt.Errorf("invalid HTTP port %d", info.Port)
			} else {
				resp, lastErr = client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/session/nope", info.Port))
			}
		}
		if resp != nil {
			break
		}
		select {
		case serveErr := <-served:
			t.Fatalf("Serve returned before REST was ready: %v", serveErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("REST did not become ready: %v", lastErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /api/session/nope status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	origin := "http://" + resp.Request.URL.Host
	crossSite := []struct {
		name, path string
		want       int
	}{
		{"setup callback is public", "/github/setup/callback?state=unknown&code=c", http.StatusBadRequest},
		{"spa shell is public", "/s/some-review", http.StatusOK},
		{"setup stays guarded", "/github/setup", http.StatusUnauthorized},
		{"api stays guarded", "/api/session/nope", http.StatusUnauthorized},
	}
	for _, c := range crossSite {
		req, err := http.NewRequest(http.MethodGet, origin+c.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		got, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		_ = got.Body.Close()
		if got.StatusCode != c.want {
			t.Errorf("%s: cross-site GET %s status = %d, want %d", c.name, c.path, got.StatusCode, c.want)
		}
	}

	cancel()
	select {
	case serveErr := <-served:
		if serveErr != nil {
			t.Fatalf("Serve: %v", serveErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
}

func TestRecapturePRReadsTheDBServeActivates(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cc-review-serve-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	testhome.Pin(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := paths.EnsureStateDir(); err != nil {
		t.Fatal(err)
	}
	ledger, err := decisions.Open(ctx, filepath.Join(home, "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	s, rv, err := newDaemon(ledger, 0)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for s.DB() == nil {
		select {
		case serveErr := <-served:
			t.Fatalf("Serve returned before activating its store: %v", serveErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("Serve did not activate its store")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := rv.recapturePR(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "review missing is not a pull-request review") {
		t.Fatalf("recapturePR = %v, want a refusal read from the activated store", err)
	}

	httpInfoPath := filepath.Join(home, ".cc-review", "v1", "http.json")
	for {
		if _, err := os.Stat(httpInfoPath); err == nil {
			break
		}
		select {
		case serveErr := <-served:
			t.Fatalf("Serve returned before it started serving: %v", serveErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("Serve did not start serving")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case serveErr := <-served:
		if serveErr != nil {
			t.Fatalf("Serve: %v", serveErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
}
