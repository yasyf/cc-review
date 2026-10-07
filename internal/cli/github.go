package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"

	ccd "github.com/yasyf/cc-interact/daemon"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/paths"
)

const (
	setupTimeout = 10 * time.Minute
	setupPoll    = 500 * time.Millisecond
)

func newGitHubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Create and inspect the cc-review GitHub App Claude replies as",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newGitHubSetupCmd(), newGitHubStatusCmd())
	return cmd
}

func newGitHubSetupCmd() *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Create your cc-review GitHub App in the browser and wait for it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := ensureCurrent(ctx); err != nil {
				return err
			}
			port, err := daemonHTTPPort()
			if err != nil {
				return err
			}
			setupPath := "/github/setup"
			if org != "" {
				setupPath += "?" + url.Values{"org": {org}}.Encode()
			}
			tailnetURLs, err := daemonTailnetURLs(ctx, setupPath)
			if err != nil {
				return err
			}
			setupURL := fmt.Sprintf("http://127.0.0.1:%d%s", port, setupPath)
			started := time.Now()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "open: "+setupURL)
			for _, u := range tailnetURLs {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "tailnet: "+u)
			}
			if err := openURL(ctx, setupURL); err != nil {
				return err
			}
			app, err := waitForApp(ctx, started)
			if err != nil {
				return err
			}
			logo, err := ghapp.WriteLogo()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "app: "+app.Slug)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "logo: "+logo)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "install: "+app.InstallURL())
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "create the app under this GitHub organization instead of your account")
	return cmd
}

func newGitHubStatusCmd() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the cc-review GitHub App and whether it is installed on this repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			app, ok, err := ghapp.Load()
			if err != nil {
				return err
			}
			if !ok {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "app: not set up; run `cc-review github setup`")
				return nil
			}
			repo, err := github.RepoFromRemote(ctx, mustCwd(cwd))
			if err != nil {
				return err
			}
			_, err = app.TokenSource(repo).Token(ctx)
			return printAppStatus(cmd.OutOrStdout(), app, repo, err)
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}

func printAppStatus(w io.Writer, app ghapp.App, repo github.Repo, tokenErr error) error {
	install := "installed on " + repo.String()
	switch {
	case errors.Is(tokenErr, ghapp.ErrNotInstalled):
		install = "not installed on " + repo.String() + "; install at " + app.InstallURL()
	case tokenErr != nil:
		return tokenErr
	}
	_, _ = fmt.Fprintln(w, "app: "+app.Slug)
	_, _ = fmt.Fprintln(w, "bot: "+app.BotLogin)
	_, _ = fmt.Fprintln(w, "install: "+install)
	return nil
}

func daemonHTTPPort() (int, error) {
	data, err := os.ReadFile(paths.App().HTTPInfoPath())
	if err != nil {
		return 0, fmt.Errorf("read daemon http info: %w", err)
	}
	var info ccd.HTTPInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return 0, fmt.Errorf("decode daemon http info: %w", err)
	}
	return info.Port, nil
}

func daemonTailnetURLs(ctx context.Context, path string) ([]string, error) {
	rc, err := daemon.NewReviewClient(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return rc.TailnetURLs(ctx, path)
}

func waitForApp(ctx context.Context, since time.Time) (ghapp.App, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	t := time.NewTicker(setupPoll)
	defer t.Stop()
	for {
		if info, err := os.Stat(paths.GitHubApp()); err == nil && !info.ModTime().Before(since) {
			app, _, err := ghapp.Load()
			return app, err
		}
		select {
		case <-ctx.Done():
			return ghapp.App{}, fmt.Errorf("waiting for %s: %w", paths.GitHubApp(), ctx.Err())
		case <-t.C:
		}
	}
}
