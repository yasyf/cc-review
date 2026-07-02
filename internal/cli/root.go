// Package cli wires the cobra command tree for cc-review: the user-facing review
// commands (start, close, list, reply, feedback, export, setup-channels) and the domain hooks
// (turn-start, turn-end) layered on cc-interact's reusable substrate commands
// (daemon, watch, status, stop, session-record, guard-edit, channel-ack, channel).
package cli

import (
	"github.com/spf13/cobra"

	"github.com/yasyf/cc-interact/cmd"

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
	d := deps()
	// The plugin's scripts/mcp-channel.sh invokes the historical `mcp-channel`
	// name; cc-interact's ChannelCmd defaults to `channel`. Preserve the plugin
	// contract and keep `channel` as an alias.
	channelCmd := cmd.ChannelCmd(d)
	channelCmd.Use = "mcp-channel"
	channelCmd.Aliases = []string{"channel"}
	root.AddCommand(
		// Substrate commands from cc-interact.
		cmd.WatchCmd(d),
		cmd.StatusCmd(d),
		cmd.StopCmd(d),
		cmd.SessionRecordCmd(d),
		cmd.GuardEditCmd(d),
		cmd.ChannelAckCmd(d),
		channelCmd,
		// cc-review domain commands.
		newDaemonCmd(),
		newStartCmd(),
		newCloseCmd(),
		newListCmd(),
		newReplyCmd(),
		newFeedbackCmd(),
		newExportCmd(),
		newTurnStartCmd(),
		newTurnEndCmd(),
		newSetupChannelsCmd(),
	)
	return root
}
