// Package cli wires the cobra command tree for cc-review: the user-facing
// commands (start, watch, reply, feedback, status, stop) plus the hidden
// daemon, hook, and MCP-channel entry points. Every command is a thin shell
// around the daemon control client.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/version"
)

// NewRootCmd builds the root command with every subcommand attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "cc-review",
		Short:         "Local code-review daemon + Claude plugin",
		Long:          "cc-review reviews the code Claude just wrote in a PR-like web UI and streams the feedback back into the running Claude session.",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(
		newStartCmd(),
		newWatchCmd(),
		newReplyCmd(),
		newFeedbackCmd(),
		newStatusCmd(),
		newStopCmd(),
		newDaemonCmd(),
		newSessionRecordCmd(),
		newChannelAckCmd(),
		newGuardEditCmd(),
		newTurnStartCmd(),
		newTurnEndCmd(),
		newMCPChannelCmd(),
		newSetupChannelsCmd(),
	)
	return root
}
