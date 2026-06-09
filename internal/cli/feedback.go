package cli

import (
	"errors"
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
			if err := daemon.EnsureRunning(startTimeout); err != nil {
				return err
			}
			resp, err := daemon.NewClient().Feedback(session, mustCwd(cwd))
			if err != nil {
				return err
			}
			if !resp.OK {
				return errors.New(resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp.Feedback))
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}
