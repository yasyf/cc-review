package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newStatusCmd() *cobra.Command {
	var (
		session string
		cwd     string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon and review status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client := daemon.NewClient()
			if !client.Available() {
				fmt.Fprintln(cmd.OutOrStdout(), "daemon: not running")
				return nil
			}
			resp, err := client.Status(session, mustCwd(cwd))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "daemon: running (%s)\n", resp.DaemonVersion)
			fmt.Fprintf(out, "http:   127.0.0.1:%d\n", resp.HTTPPort)
			if resp.ReviewID != "" {
				fmt.Fprintf(out, "review: %s (%s)\n", resp.ReviewID, resp.Status)
			} else {
				fmt.Fprintln(out, "review: none for this session/repo")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client := daemon.NewClient()
			if !client.Available() {
				fmt.Fprintln(cmd.OutOrStdout(), "daemon: not running")
				return nil
			}
			if _, err := client.Shutdown(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "daemon: stopping")
			return nil
		},
	}
}
