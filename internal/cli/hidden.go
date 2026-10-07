package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-interact/vcs"
	"github.com/yasyf/daemonkit"
	"github.com/yasyf/synckit/meshtrust"

	"github.com/yasyf/cc-review/internal/daemon"
)

// devHTTPPort is the fixed port the daemon binds under --dev so the Vite dev
// server's proxy can reach the API/SSE plane.
const devHTTPPort = 8787

const httpPortEnv = "CC_REVIEW_HTTP_PORT"

// newDaemonCmd is the hidden entry point the lazy-start spawns. --dev pins the
// HTTP plane to a known port for the Vite dev proxy; the lazily-spawned daemon
// (Args=["daemon"], no --dev) binds CC_REVIEW_HTTP_PORT when its environment
// sets one, and an ephemeral port otherwise. When synckit's mesh state exists
// the daemon also serves its tailnet addresses.
func newDaemonCmd() *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Run the background daemon",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := daemonkit.CloseInheritedFDs(); err != nil {
				return err
			}
			port, err := httpPort(dev)
			if err != nil {
				return err
			}
			return daemon.Serve(cmd.Context(), port, meshtrust.Detect())
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "bind the HTTP plane to a fixed port for the Vite dev proxy")
	return cmd
}

func httpPort(dev bool) (int, error) {
	if dev {
		return devHTTPPort, nil
	}
	raw := os.Getenv(httpPortEnv)
	if raw == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", httpPortEnv, err)
	}
	return port, nil
}

// newTurnStartCmd is the hidden UserPromptSubmit hook handler: it opens a turn
// with the pre-edit working-tree snapshot.
func newTurnStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "turn-start",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runTurnHook(cmd, func(ctx context.Context, session, cwd, prompt string) error {
				rc, err := daemon.NewReviewClient(ctx)
				if err != nil {
					return err
				}
				defer func() { _ = rc.Close() }()
				return rc.TurnStart(ctx, session, cwd, prompt)
			})
			return nil
		},
	}
}

// newTurnEndCmd is the hidden Stop hook handler: it closes the open turn with the
// post-edit working-tree snapshot.
func newTurnEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "turn-end",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runTurnHook(cmd, func(ctx context.Context, session, cwd, _ string) error {
				rc, err := daemon.NewReviewClient(ctx)
				if err != nil {
					return err
				}
				defer func() { _ = rc.Close() }()
				return rc.TurnEnd(ctx, session, cwd)
			})
			return nil
		},
	}
}

// runTurnHook drives both turn hooks: skip outside a repo, then send the turn
// request, swallowing every failure — UserPromptSubmit stdout is injected into
// Claude's context, so nothing may be printed.
func runTurnHook(cmd *cobra.Command, send func(ctx context.Context, session, cwd, prompt string) error) {
	in := readHookInput(cmd.InOrStdin())
	if _, err := vcs.Root(cmd.Context(), in.Cwd); err != nil {
		return
	}
	// Deliberate exception to hooks using EnsureCurrentIfRunning: always-on turn
	// recording must boot the daemon.
	launcher, err := launcher()
	if err != nil {
		return
	}
	if err := launcher.EnsureCurrent(cmd.Context(), 15*time.Second); err != nil {
		return
	}
	_ = send(cmd.Context(), in.SessionID, in.Cwd, in.Prompt)
}
