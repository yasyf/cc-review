package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/daemon"
)

func newListCmd() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every open or expired review with its status, age, idle time, and repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := ensureCurrent(ctx); err != nil {
				return err
			}
			reviews, err := daemon.NewReviewClient().List(ctx, "", mustCwd(cwd))
			if err != nil {
				return err
			}
			for _, line := range listLines(reviews, time.Now()) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	return cmd
}

// listLines renders the reviews as aligned SLUG/STATUS/AGE/IDLE/SCOPE rows.
func listLines(reviews []daemon.ReviewInfo, now time.Time) []string {
	if len(reviews) == 0 {
		return []string{"no open reviews"}
	}
	slugW, statusW, ageW, idleW := len("SLUG"), len("STATUS"), len("AGE"), len("IDLE")
	rows := make([][4]string, len(reviews))
	for i, r := range reviews {
		rows[i] = [4]string{r.Slug, r.Status, age(now.Sub(r.CreatedAt)), age(now.Sub(r.LastActivity))}
		slugW, statusW = max(slugW, len(rows[i][0])), max(statusW, len(rows[i][1]))
		ageW, idleW = max(ageW, len(rows[i][2])), max(idleW, len(rows[i][3]))
	}
	lines := make([]string, 0, len(reviews)+1)
	lines = append(lines, fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %s", slugW, "SLUG", statusW, "STATUS", ageW, "AGE", idleW, "IDLE", "SCOPE"))
	for i, r := range reviews {
		lines = append(lines, fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %s", slugW, rows[i][0], statusW, rows[i][1], ageW, rows[i][2], idleW, rows[i][3], r.Scope))
	}
	return lines
}
