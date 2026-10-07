package ghapp

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/yasyf/cc-review/internal/github"
)

const (
	homepage = "https://github.com/yasyf/cc-review"
	stateTTL = 10 * time.Minute
)

var setupPage = template.Must(template.New("setup").Parse(`<!doctype html>
<title>Create the cc-review GitHub App</title>
<form id="manifest" method="post" action="{{.Action}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<noscript><button type="submit">Create {{.Name}} on GitHub</button></noscript>
</form>
<script>document.getElementById("manifest").submit()</script>
`))

var states = newNonces[struct{}](stateTTL)

type manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     hookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

type hookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

type conversion struct {
	ID    int64  `json:"id"`
	Slug  string `json:"slug"`
	PEM   string `json:"pem"`
	Owner struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

// SetupHandler serves GET /github/setup[?org=<org>], auto-submitting the app
// manifest to GitHub under a single-use state nonce the callback verifies.
func SetupHandler(user *github.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login, err := user.Viewer(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("resolve github viewer: %v", err), http.StatusBadGateway)
			return
		}
		state := states.issue(struct{}{})
		action := "https://github.com/settings/apps/new"
		if org := r.URL.Query().Get("org"); org != "" {
			action = "https://github.com/organizations/" + url.PathEscape(org) + "/settings/apps/new"
		}
		m := manifest{
			Name:           "cc-review-" + login,
			URL:            homepage,
			HookAttributes: hookAttributes{URL: homepage, Active: false},
			RedirectURL:    origin(r) + "/github/setup/callback",
			DefaultPermissions: map[string]string{
				"pull_requests": "write", "contents": "read", "checks": "read", "metadata": "read",
			},
			DefaultEvents: []string{},
		}
		raw, err := json.Marshal(m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = setupPage.Execute(w, map[string]string{
			"Action": action + "?state=" + url.QueryEscape(state), "Manifest": string(raw), "Name": m.Name,
		})
	})
}

// CallbackHandler serves GET /github/setup/callback, exchanging the manifest
// code for the app's credentials, saving them, and redirecting to the
// picture page PictureHandler serves.
func CallbackHandler(user *github.Client, onDone func(App)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if !states.consume(q.Get("state")) {
			http.Error(w, "unknown or expired setup state; run `cc-review github setup` again", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		var conv conversion
		if err := user.REST(r.Context(), http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", nil, &conv); err != nil {
			http.Error(w, fmt.Sprintf("exchange manifest code: %v", err), http.StatusBadGateway)
			return
		}
		app := App{ID: conv.ID, Slug: conv.Slug, BotLogin: conv.Slug + "[bot]", Owner: conv.Owner.Login}
		if err := Save(app, conv.PEM); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		onDone(app)
		grant := pictures.issue(picture{app: app, settings: conv.settingsURL()})
		http.Redirect(w, r, "/github/setup/picture?"+url.Values{"state": {grant}}.Encode(), http.StatusSeeOther)
	})
}

func origin(r *http.Request) string {
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

func (c conversion) settingsURL() string {
	if c.Owner.Type == "Organization" {
		return "https://github.com/organizations/" + url.PathEscape(c.Owner.Login) + "/settings/apps/" + url.PathEscape(c.Slug)
	}
	return "https://github.com/settings/apps/" + url.PathEscape(c.Slug)
}
