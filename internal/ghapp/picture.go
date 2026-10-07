package ghapp

import (
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/yasyf/cc-review/internal/paths"
)

// BadgeColor is the badge background color that matches the logo's backdrop.
const BadgeColor = "#DD9040"

const pictureTTL = time.Hour

//go:embed github-app-logo.png
var logo []byte

var pictures = newNonces[picture](pictureTTL)

type picture struct {
	app      App
	settings string
}

var picturePage = template.Must(template.New("picture").Parse(`<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Finish setting up {{.Slug}}</title>
<style>
:root { color-scheme: light dark; font-family: system-ui, sans-serif; }
body { max-width: 40rem; margin: 3rem auto; padding: 0 1.5rem; line-height: 1.5; }
ol { padding: 0; list-style: none; counter-reset: step; }
li { counter-increment: step; margin: 2rem 0; padding: 1.5rem; border: 1px solid #8884; border-radius: 12px; }
h2::before { content: "Step " counter(step) ": "; }
h2 { margin-top: 0; font-size: 1.15rem; }
img { display: block; width: 160px; height: 160px; border-radius: 50%; margin: 1rem 0; }
code { font-size: 1rem; }
.swatch { display: inline-block; width: 1em; height: 1em; border-radius: 4px; vertical-align: -0.15em; background: {{.BadgeColor}}; }
.actions { display: flex; gap: 0.75rem; flex-wrap: wrap; margin-top: 1rem; }
.button { padding: 0.5rem 1rem; border-radius: 8px; border: 1px solid #8886; text-decoration: none; color: inherit; font-weight: 600; }
.primary { background: #1f883d; border-color: #1f883d; color: #fff; }
</style>
<h1>{{.Slug}} is ready</h1>
<ol>
<li>
<h2>Set the app's picture</h2>
<img src="{{.Logo}}" alt="The cc-review owl">
<p>GitHub only takes an app's picture through its settings page. Download the logo, open the settings, and drop it on <b>Upload a logo</b> under Display information. Then set <b>Badge background color</b> to <code>{{.BadgeColor}}</code> <span class="swatch"></span>.</p>
<div class="actions">
<a class="button" href="{{.Logo}}" download="{{.Slug}}.png">Download logo</a>
<a class="button" href="{{.Settings}}" target="_blank" rel="noopener">Open app settings</a>
</div>
</li>
<li>
<h2>Install the app</h2>
<p>Install it on the repositories whose pull requests you review.</p>
<div class="actions"><a class="button primary" href="{{.Install}}">Install {{.Slug}}</a></div>
</li>
</ol>
`))

// PictureHandler serves GET /github/setup/picture, the page the callback lands
// on: set the app's picture, then install the app.
func PictureHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		p, ok := pictures.lookup(state)
		if !ok {
			http.Error(w, "unknown or expired setup state; run `cc-review github setup` again", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = picturePage.Execute(w, map[string]string{
			"Slug":       p.app.Slug,
			"Logo":       "/github/setup/logo.png?" + url.Values{"state": {state}}.Encode(),
			"Settings":   p.settings,
			"Install":    p.app.InstallURL(),
			"BadgeColor": BadgeColor,
		})
	})
}

// LogoHandler serves GET /github/setup/logo.png, the app's avatar, under the
// same state PictureHandler accepts.
func LogoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := pictures.lookup(r.URL.Query().Get("state")); !ok {
			http.Error(w, "unknown or expired setup state; run `cc-review github setup` again", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(logo)
	})
}

// WriteLogo writes the app's avatar PNG into the state dir and returns its path.
func WriteLogo() (string, error) {
	if err := paths.EnsureStateDir(); err != nil {
		return "", err
	}
	path := paths.GitHubAppLogo()
	if err := os.WriteFile(path, logo, 0o600); err != nil {
		return "", fmt.Errorf("write github app logo: %w", err)
	}
	return path, nil
}
