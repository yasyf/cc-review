package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newStartCmd() *cobra.Command {
	var (
		session string
		cwd     string
		fresh   bool
		base    string
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start or resume a review of the working tree and print its URL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := ensureCurrent(ctx); err != nil {
				return err
			}
			started, err := daemon.NewReviewClient().Start(ctx, session, mustCwd(cwd), fresh, base)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), started.URL)
			offer, reason, offerErr := channelsOffer()
			for _, line := range startExtraLines(started.ChannelState, offer, reason, offerErr, started.AIRequests) {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id (keys the review with the repo root)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	cmd.Flags().BoolVar(&fresh, "new", false, "force a fresh review, detaching any existing one for this session")
	cmd.Flags().StringVar(&base, "base", "", "pin a new review's diff base: the fork point of this ref and the working copy (default: HEAD, falling back to trunk when the working tree is clean)")
	return cmd
}

// startExtraLines renders the channel: and setup: lines (always) and one
// organize: line per open request the daemon re-offered (the eager system
// organize plus any human AI-bar prompts left pending). An offer error degrades
// to offer=false with the error as the reason — start never fails on the setup
// check.
func startExtraLines(channelState string, offer bool, reason string, offerErr error, organizes []json.RawMessage) []string {
	if offerErr != nil {
		offer, reason = false, offerErr.Error()
	}
	setup, _ := json.Marshal(map[string]any{"offer": offer, "reason": reason})
	lines := []string{"channel: " + channelState, "setup: " + string(setup)}
	for _, organize := range organizes {
		if len(organize) > 0 {
			lines = append(lines, "organize: "+string(organize))
		}
	}
	return lines
}
