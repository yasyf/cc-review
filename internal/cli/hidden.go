package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/daemon"
)

// devHTTPPort is the fixed port the daemon binds under --dev so the Vite dev
// server's proxy can reach the API/SSE plane.
const devHTTPPort = 8787

// newDaemonCmd is the hidden entry point the lazy-start spawns. --dev pins the
// HTTP plane to a known port for the Vite dev proxy; the lazily-spawned daemon
// (Args=["daemon"], no --dev) binds an ephemeral port.
func newDaemonCmd() *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Run the background daemon",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			port := 0
			if dev {
				port = devHTTPPort
			}
			return daemon.Serve(cmd.Context(), port)
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "bind the HTTP plane to a fixed port for the Vite dev proxy")
	return cmd
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
				return daemon.NewReviewClient().TurnStart(ctx, session, cwd, prompt)
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
				return daemon.NewReviewClient().TurnEnd(ctx, session, cwd)
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
	if err := launcher().EnsureCurrent(15 * time.Second); err != nil {
		return
	}
	_ = send(cmd.Context(), in.SessionID, in.Cwd, in.Prompt)
}
