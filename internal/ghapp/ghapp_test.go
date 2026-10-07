package ghapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zalando/go-keyring"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/testhome"
)

var (
	t0            = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tokenLifetime = time.Hour
	manifestValue = regexp.MustCompile(`name="manifest" value="([^"]*)"`)
	formAction    = regexp.MustCompile(`action="([^"]*)"`)
)

type fakeGitHub struct {
	srv           *httptest.Server
	key           *rsa.PrivateKey
	installed     map[string]int64
	lookups       atomic.Int32
	mints         atomic.Int32
	conversions   atomic.Int32
	authorization atomic.Value
}

func newFakeGitHub(t *testing.T, key *rsa.PrivateKey) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{key: key, installed: map[string]int64{"acme/widgets": 42}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{name}/installation", func(w http.ResponseWriter, r *http.Request) {
		f.lookups.Add(1)
		f.authorization.Store(r.Header.Get("Authorization"))
		id, ok := f.installed[r.PathValue("owner")+"/"+r.PathValue("name")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		n := f.mints.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": fmt.Sprintf("ghs_%s_%d", r.PathValue("id"), n), "expires_at": now().Add(tokenLifetime).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		f.conversions.Add(1)
		if r.PathValue("code") != "good-code" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 7, "slug": "cc-review-octo", "pem": pemOf(key), "owner": map[string]any{"login": "octo"},
		})
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"octo"}}}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) client() *github.Client {
	return github.New(staticToken("user-token"), github.WithBaseURL(f.srv.URL, f.srv.URL+"/graphql"))
}

type staticToken string

func (s staticToken) Token(_ context.Context) (string, error) { return string(s), nil }

func pemOf(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func setup(t *testing.T) (*fakeGitHub, *rsa.PrivateKey, *time.Time) {
	t.Helper()
	testhome.Temp(t)
	keyring.MockInit()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	clock := t0
	prevNow, prevOpts := now, appClientOptions
	now = func() time.Time { return clock }
	f := newFakeGitHub(t, key)
	appClientOptions = []github.Option{github.WithBaseURL(f.srv.URL, f.srv.URL+"/graphql")}
	cache.Lock()
	cache.installations, cache.tokens = map[installKey]int64{}, map[int64]installationToken{}
	cache.Unlock()
	t.Cleanup(func() { now, appClientOptions = prevNow, prevOpts })
	return f, key, &clock
}

func installApp(t *testing.T, key *rsa.PrivateKey) App {
	t.Helper()
	app := App{ID: 7, Slug: "cc-review-octo", BotLogin: "cc-review-octo[bot]", Owner: "octo"}
	if err := Save(app, pemOf(key)); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestSignJWT(t *testing.T) {
	_, key, _ := setup(t)
	app := installApp(t, key)
	signed, err := app.signJWT(t0)
	if err != nil {
		t.Fatal(err)
	}
	var claims jwt.RegisteredClaims
	_, err = jwt.ParseWithClaims(signed, &claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithTimeFunc(func() time.Time { return t0 }))
	if err != nil {
		t.Fatalf("verify jwt: %v", err)
	}
	if claims.Issuer != "7" {
		t.Errorf("iss = %q, want %q", claims.Issuer, "7")
	}
	if got, want := claims.IssuedAt.Time, t0.Add(-60*time.Second); !got.Equal(want) {
		t.Errorf("iat = %v, want %v", got, want)
	}
	if got, want := claims.ExpiresAt.Time, t0.Add(9*time.Minute); !got.Equal(want) {
		t.Errorf("exp = %v, want %v", got, want)
	}

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jwt.Parse(signed, func(*jwt.Token) (any, error) { return &other.PublicKey, nil },
		jwt.WithTimeFunc(func() time.Time { return t0 })); !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Errorf("verify with a foreign key: err = %v, want %v", err, jwt.ErrTokenSignatureInvalid)
	}
}

func TestTokenSourceCachesUntilRefreshWindow(t *testing.T) {
	f, key, clock := setup(t)
	ts := installApp(t, key).TokenSource(github.Repo{Owner: "acme", Name: "widgets"})
	refreshAt := t0.Add(tokenLifetime - 5*time.Minute)
	steps := []struct {
		name      string
		at        time.Time
		wantToken string
		wantMints int32
	}{
		{"first call mints", t0, "ghs_42_1", 1},
		{"reuses before the refresh window", refreshAt.Add(-time.Second), "ghs_42_1", 1},
		{"re-mints at the refresh window", refreshAt, "ghs_42_2", 2},
		{"reuses the fresh token", refreshAt.Add(time.Minute), "ghs_42_2", 2},
	}
	for _, s := range steps {
		*clock = s.at
		got, err := ts.Token(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got != s.wantToken || f.mints.Load() != s.wantMints {
			t.Errorf("%s: token %q after %d mints, want %q after %d", s.name, got, f.mints.Load(), s.wantToken, s.wantMints)
		}
	}
	if n := f.lookups.Load(); n != 1 {
		t.Errorf("installation lookups = %d, want 1", n)
	}
	auth, _ := f.authorization.Load().(string)
	if _, err := jwt.Parse(strings.TrimPrefix(auth, "Bearer "), func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithTimeFunc(func() time.Time { return t0 })); err != nil {
		t.Errorf("installation lookup bearer %q is not the app jwt: %v", auth, err)
	}
}

func TestTokenSourceNotInstalled(t *testing.T) {
	f, key, _ := setup(t)
	app := installApp(t, key)
	_, err := app.TokenSource(github.Repo{Owner: "acme", Name: "gadgets"}).Token(t.Context())
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
	if !strings.Contains(err.Error(), "https://github.com/apps/cc-review-octo/installations/new") {
		t.Errorf("err %q does not name the install URL", err)
	}
	if n := f.mints.Load(); n != 0 {
		t.Errorf("mints = %d, want 0", n)
	}
}

func renderSetup(t *testing.T, f *fakeGitHub, target string) (action string, m manifest) {
	t.Helper()
	rec := httptest.NewRecorder()
	SetupHandler(f.client()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	value := manifestValue.FindStringSubmatch(body)
	act := formAction.FindStringSubmatch(body)
	if value == nil || act == nil {
		t.Fatalf("setup page has no manifest form: %s", body)
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(value[1])), &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return html.UnescapeString(act[1]), m
}

func TestSetupHandler(t *testing.T) {
	cases := []struct {
		name, target, wantPath, wantRedirect string
	}{
		{"personal account", "http://127.0.0.1:4321/github/setup", "/settings/apps/new", "http://127.0.0.1:4321/github/setup/callback"},
		{"organization", "http://127.0.0.1:4321/github/setup?org=acme", "/organizations/acme/settings/apps/new", "http://127.0.0.1:4321/github/setup/callback"},
		{"tailnet label", "http://devbox:4321/github/setup", "/settings/apps/new", "http://devbox:4321/github/setup/callback"},
		{"tailnet tls", "https://devbox.tail1234.ts.net:4321/github/setup", "/settings/apps/new", "https://devbox.tail1234.ts.net:4321/github/setup/callback"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _, _ := setup(t)
			action, m := renderSetup(t, f, c.target)
			u, err := url.Parse(action)
			if err != nil {
				t.Fatal(err)
			}
			if u.Scheme != "https" || u.Host != "github.com" || u.Path != c.wantPath {
				t.Errorf("action = %q, want https://github.com%s", action, c.wantPath)
			}
			if len(u.Query().Get("state")) != 64 {
				t.Errorf("state = %q, want a 64-char nonce", u.Query().Get("state"))
			}
			want := manifest{
				Name:           "cc-review-octo",
				URL:            "https://github.com/yasyf/cc-review",
				HookAttributes: hookAttributes{URL: "https://github.com/yasyf/cc-review", Active: false},
				RedirectURL:    c.wantRedirect,
				DefaultPermissions: map[string]string{
					"pull_requests": "write", "contents": "read", "checks": "read", "metadata": "read",
				},
				DefaultEvents: []string{},
			}
			gotJSON, _ := json.Marshal(m)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("manifest = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestCallbackHandler(t *testing.T) {
	f, key, clock := setup(t)
	issue := func() string {
		action, _ := renderSetup(t, f, "http://127.0.0.1:4321/github/setup")
		u, _ := url.Parse(action)
		return u.Query().Get("state")
	}
	var done []App
	h := CallbackHandler(f.client(), func(a App) { done = append(done, a) })
	callback := func(state, code string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/github/setup/callback?"+url.Values{"state": {state}, "code": {code}}.Encode(), nil))
		return rec
	}

	state := issue()
	rec := callback(state, "good-code")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "https://github.com/apps/cc-review-octo/installations/new" {
		t.Fatalf("callback = %d Location %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	want := App{ID: 7, Slug: "cc-review-octo", BotLogin: "cc-review-octo[bot]", Owner: "octo"}
	app, ok, err := Load()
	if err != nil || !ok || app != want {
		t.Fatalf("Load() = %+v, %v, %v; want %+v", app, ok, err, want)
	}
	if len(done) != 1 || done[0] != want {
		t.Errorf("onDone calls = %+v, want [%+v]", done, want)
	}
	if stored, err := keyring.Get("cc-review.github-app", "7"); err != nil || stored != pemOf(key) {
		t.Errorf("keychain pem = %q, %v; want the converted pem", stored, err)
	}

	expired := issue()
	*clock = clock.Add(10*time.Minute + time.Second)
	rejected := []struct{ name, state, code string }{
		{"replayed state", state, "good-code"},
		{"unknown state", strings.Repeat("0", 64), "good-code"},
		{"expired state", expired, "good-code"},
	}
	for _, c := range rejected {
		if rec := callback(c.state, c.code); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", c.name, rec.Code)
		}
	}
	if n := f.conversions.Load(); n != 1 {
		t.Errorf("conversions = %d, want 1", n)
	}

	if rec := callback(issue(), "bad-code"); rec.Code != http.StatusBadGateway {
		t.Errorf("bad code: status = %d, want 502", rec.Code)
	}
}

func TestLoadMissing(t *testing.T) {
	testhome.Temp(t)
	app, ok, err := Load()
	if err != nil || ok || app != (App{}) {
		t.Errorf("Load() = %+v, %v, %v; want zero, false, nil", app, ok, err)
	}
}
