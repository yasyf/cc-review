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

// newSessionRecordCmd is the hidden SessionStart hook handler. It records the
// session's facts best-effort: if the daemon is not up it does nothing (start
// will resolve the session later regardless).
func newSessionRecordCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "session-record",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := readHookInput(cmd.InOrStdin())
			client := daemon.NewClient()
			if !client.Available() {
				return nil
			}
			_, _ = client.SessionRecord(in.SessionID, in.Cwd, in.TranscriptPath, 0)
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
