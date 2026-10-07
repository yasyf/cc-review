package outbound

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/github"
)

// ErrNoApp reports a Claude write in a PR review before the cc-review GitHub
// App exists.
var ErrNoApp = errors.New("the cc-review GitHub App is not set up: run `cc-review github setup`")

// GitHubApp is the AppClient backed by github-app.json. It mints a token up
// front, so an uninstalled repo fails before anything is written.
func GitHubApp(opts ...github.Option) AppClient {
	var (
		mu      sync.Mutex
		sources = make(map[string]github.TokenSource)
	)
	return func(ctx context.Context, repo github.Repo) (*github.Client, error) {
		app, ok, err := ghapp.Load()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNoApp
		}
		key := fmt.Sprintf("%d/%s", app.ID, repo)
		mu.Lock()
		ts, ok := sources[key]
		if !ok {
			ts = app.TokenSource(repo)
			sources[key] = ts
		}
		mu.Unlock()
		if _, err := ts.Token(ctx); err != nil {
			return nil, err
		}
		return github.New(ts, opts...), nil
	}
}
