package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newCloseCmd() *cobra.Command {
	var (
		session string
		cwd     string
		stale   bool
	)
	cmd := &cobra.Command{
		Use:   "close [review]",
		Short: "Close a review without submitting: this window's, any review by slug/id, or every stale one with --stale",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			if stale && ref != "" {
				return errors.New("--stale closes every stale review; drop the explicit review argument")
			}
			ctx := cmd.Context()
			if err := ensureCurrent(ctx); err != nil {
				return err
			}
			rc, err := daemon.NewReviewClient(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			closed, err := rc.CloseReview(ctx, session, mustCwd(cwd), ref, stale)
			if err != nil {
				return err
			}
			for _, line := range closeLines(closed, stale, time.Now()) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id (keys the review with the repo root)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	cmd.Flags().BoolVar(&stale, "stale", false, "close every expired review across all repos, sweeping idle ones past the TTL first")
	return cmd
}

// closeLines renders one line per closed review; the stale sweep carries each
// review's idle time and repo, a single close just the slug.
func closeLines(closed []daemon.ReviewInfo, stale bool, now time.Time) []string {
	if !stale {
		lines := make([]string, len(closed))
		for i, r := range closed {
			lines[i] = "closed " + r.Slug
		}
		return lines
	}
	if len(closed) == 0 {
		return []string{"nothing stale"}
	}
	lines := make([]string, len(closed))
	for i, r := range closed {
		lines[i] = fmt.Sprintf("closed %s  idle %s  %s", r.Slug, age(now.Sub(r.LastActivity)), r.Scope)
	}
	return lines
}

// age renders a duration as the largest two units, floor 0m.
func age(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", d/(24*time.Hour), d%(24*time.Hour)/time.Hour)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", d/time.Hour, d%time.Hour/time.Minute)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return "0m"
	}
}
