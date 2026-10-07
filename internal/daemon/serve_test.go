package daemon

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/ghapp/ghapptest"
	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/testhome"
)

var setupState = regexp.MustCompile(`state=([0-9a-f]{64})`)

func fakeManifestGitHub(t *testing.T) *github.Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"octo"}}}`))
	})
	mux.HandleFunc("POST /app-manifests/good-code/conversions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 7, "slug": "cc-review-octo", "pem": ghapptest.PEM(key), "owner": map[string]any{"login": "octo"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return github.New(staticToken("user-token"), github.WithBaseURL(srv.URL, srv.URL+"/graphql"))
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func TestServeMountsRESTWithActivatedDB(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cc-review-serve-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	testhome.Pin(t, home)
	t.Setenv("CC_DECISIONS_DB", filepath.Join(home, "decisions.db"))
	keyring.MockInit()
	prevGitHub := userGitHub
	fake := fakeManifestGitHub(t)
	userGitHub = func() *github.Client { return fake }
	t.Cleanup(func() { userGitHub = prevGitHub })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, 0, nil) }()

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
	noRedirect := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	get := func(path string, crossSite bool) (int, string, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, origin+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if crossSite {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		got, err := noRedirect.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = got.Body.Close() }()
		body, _ := io.ReadAll(got.Body)
		return got.StatusCode, got.Header.Get("Location"), string(body)
	}

	code, _, page := get("/github/setup", false)
	state := setupState.FindStringSubmatch(page)
	if code != http.StatusOK || state == nil {
		t.Fatalf("GET /github/setup = %d with no state: %s", code, page)
	}
	callback := "/github/setup/callback?code=good-code&state=" + state[1]
	if code, location, body := get(callback, true); code != http.StatusSeeOther || location != "https://github.com/apps/cc-review-octo/installations/new" {
		t.Fatalf("cross-site callback with a valid state = %d Location %q: %s", code, location, body)
	}
	if app, ok, err := ghapp.Load(); err != nil || !ok || app.BotLogin != "cc-review-octo[bot]" {
		t.Fatalf("Load() after callback = %+v, %v, %v", app, ok, err)
	}

	crossSite := []struct {
		name, path string
		want       int
	}{
		{"replayed state is rejected by the handler", callback, http.StatusBadRequest},
		{"unknown state is rejected by the handler", "/github/setup/callback?state=unknown&code=good-code", http.StatusBadRequest},
		{"missing state is rejected by the handler", "/github/setup/callback?code=good-code", http.StatusBadRequest},
		{"spa shell is public", "/s/some-review", http.StatusOK},
		{"setup stays guarded", "/github/setup", http.StatusUnauthorized},
		{"api stays guarded", "/api/session/nope", http.StatusUnauthorized},
	}
	for _, c := range crossSite {
		code, _, body := get(c.path, true)
		if code != c.want {
			t.Errorf("%s: cross-site GET %s status = %d, want %d", c.name, c.path, code, c.want)
		}
		if c.want == http.StatusBadRequest && !strings.Contains(body, "setup state") {
			t.Errorf("%s: body %q is not the callback's state rejection", c.name, body)
		}
	}

	rc, err := NewReviewClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	urls, err := rc.TailnetURLs(ctx, "/github/setup")
	_ = rc.Close()
	if err != nil || len(urls) != 0 {
		t.Fatalf("TailnetURLs without mesh trust = %q, %v; want none", urls, err)
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
	s, rv, err := newDaemon(ctx, ledger, 0, nil)
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
