package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newStartCmd() *cobra.Command {
	var (
		session string
		cwd     string
		fresh   bool
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start or resume a review of the working tree and print its URL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := daemon.EnsureCurrent(daemon.UpgradeTimeout); err != nil {
				return err
			}
			resp, err := daemon.NewClient().Start(daemon.Request{
				Session: session, Cwd: mustCwd(cwd), New: fresh,
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return errors.New(resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), resp.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id (keys the review with the repo root)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	cmd.Flags().BoolVar(&fresh, "new", false, "force a fresh review, detaching any existing one for this session")
	return cmd
}
