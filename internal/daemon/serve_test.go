package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

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
