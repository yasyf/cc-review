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
			rc, err := daemon.NewReviewClient(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			fb, err := rc.Feedback(ctx, session, mustCwd(cwd))
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(fb))
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}
