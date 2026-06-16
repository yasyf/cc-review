package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newFeedbackCmd() *cobra.Command {
	var (
		session string
		cwd     string
	)
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "Print the frozen feedback JSON (threads + open questions) after Submit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := ensureCurrent(ctx); err != nil {
				return err
			}
			fb, err := daemon.NewReviewClient().Feedback(ctx, session, mustCwd(cwd))
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(fb))
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}
