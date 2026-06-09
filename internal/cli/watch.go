package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newWatchCmd() *cobra.Command {
	var (
		session string
		cwd     string
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream review events as line-delimited JSON (one event per line)",
		Long: "watch prints one JSON event per line as the human comments, then exits on the\n" +
			"terminal submit event. It is meant to run under a Claude Code Monitor so each line\n" +
			"becomes a chat notification and Claude reacts per event. Output is line-buffered and\n" +
			"resumes from a persisted cursor, so restarting it re-delivers nothing it already emitted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := daemon.EnsureRunning(startTimeout); err != nil {
				return err
			}
			reviewID, port, token, err := resolveReview(ctx, daemon.NewClient(), session, mustCwd(cwd))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			return ConsumeEvents(ctx, port, token, reviewID, "watch", func(_ int64, data string) (bool, error) {
				// A failed write must propagate so the cursor doesn't advance past
				// an undelivered event (at-least-once).
				if _, err := fmt.Fprintln(out, data); err != nil {
					return false, err
				}
				return eventType(data) == "submit", nil
			})
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}
