package outbound

import (
	"context"
	"errors"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/github"
)

// ErrNoApp reports a Claude write in a PR review before the cc-review GitHub
// App exists.
var ErrNoApp = errors.New("the cc-review GitHub App is not set up: run `cc-review github setup`")

// GitHubApp is the AppClient backed by github-app.json. It mints a token up
// front, so an uninstalled repo fails before anything is written.
func GitHubApp(opts ...github.Option) AppClient {
	return func(ctx context.Context, repo github.Repo) (*github.Client, error) {
		app, ok, err := ghapp.Load()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNoApp
		}
		ts := app.TokenSource(repo)
		if _, err := ts.Token(ctx); err != nil {
			return nil, err
		}
		return github.New(ts, opts...), nil
	}
}
