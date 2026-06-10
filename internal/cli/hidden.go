package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

// devHTTPPort is the fixed port the daemon binds under --dev so the Vite dev
// server's proxy can reach the API/SSE plane.
const devHTTPPort = 8787

// newDaemonCmd is the hidden entry point the lazy-start spawns.
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
			return daemon.Run(cmd.Context(), port)
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "bind the HTTP plane to a fixed port for the Vite dev proxy")
	return cmd
}

// newSessionRecordCmd is the hidden SessionStart hook handler: it rebinds the
// window's review to the rotated session id. Claude Code fires SessionStart
// (startup/resume/clear/compact) before any tool use in the new session, so
// the rebind lands before the first guard-edit. The hook's source field is
// deliberately not parsed — pid identity makes it redundant. Best-effort: with
// no daemon running there is nothing to rebind (start resolves the window
// later regardless), and a hook must never boot one.
func newSessionRecordCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "session-record",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := readHookInput(cmd.InOrStdin())
			if err := daemon.EnsureCurrentIfRunning(); err != nil {
				return nil
			}
			_, _ = daemon.NewClient().SessionRecord(in.SessionID, in.Cwd)
			return nil
		},
	}
}

// newGuardEditCmd is the hidden PreToolUse(Edit|Write|NotebookEdit) hook handler.
// It denies edits (exit 2, the PreToolUse block signal) while an open review is
// awaiting feedback, and fails open if the daemon is unreachable.
func newGuardEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard-edit",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := readHookInput(cmd.InOrStdin())
			resp, err := daemon.NewClient().GuardEdit(in.SessionID, in.Cwd)
			if err != nil {
				return nil // daemon down: nothing to guard
			}
			if resp.OK && !resp.Allow {
				fmt.Fprintln(os.Stderr, resp.Reason)
				os.Exit(2)
			}
			return nil
		},
	}
}
