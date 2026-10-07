package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-interact/channelsetup"

	"github.com/yasyf/cc-review/internal/daemon"
	"github.com/yasyf/cc-review/internal/github"
)

func channelsOffer() (bool, string, error) {
	managedPath, err := channelsetup.ManagedSettingsPath()
	if err != nil {
		return false, "", err
	}
	d := deps()
	return channelsetup.Offer(reviewPlugin, d.Paths.ChannelSetupMarkerPath(), managedPath)
}

func newStartCmd() *cobra.Command {
	var (
		session string
		cwd     string
		fresh   bool
		base    string
		pr      string
		open    bool
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start or resume a review of the working tree or a GitHub pull request and print its URL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			dir := mustCwd(cwd)
			var ref *github.PRRef
			if pr != "" {
				r, err := resolvePRRef(ctx, pr, dir)
				if err != nil {
					return err
				}
				ref = &r
			}
			if err := ensureCurrent(ctx); err != nil {
				return err
			}
			rc, err := daemon.NewReviewClient(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			started, err := rc.Start(ctx, session, dir, fresh, base, ref)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), started.URL)
			offer, reason, offerErr := channelsOffer()
			for _, line := range startExtraLines(started, offer, reason, offerErr) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			if open {
				return openURL(ctx, started.URL)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Claude session id (keys the review with the repo root)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory (defaults to the current directory)")
	cmd.Flags().BoolVar(&fresh, "new", false, "force a fresh review, detaching any existing one for this session")
	cmd.Flags().StringVar(&base, "base", "", "pin a new review's diff base: the fork point of this ref and the working copy (default: HEAD, falling back to trunk when the working tree is clean)")
	cmd.Flags().StringVar(&pr, "pr", "", "review a GitHub pull request and its stack: a PR URL, owner/name#N, #N, or N (bare forms use the cwd's origin remote)")
	cmd.Flags().BoolVar(&open, "open", false, "open the review in the browser after printing its URL")
	cmd.MarkFlagsMutuallyExclusive("pr", "base")
	return cmd
}

func resolvePRRef(ctx context.Context, raw, dir string) (github.PRRef, error) {
	if strings.Contains(raw, "/") {
		return github.ParsePRRef(raw, nil)
	}
	repo, err := github.RepoFromRemote(ctx, dir)
	if err != nil {
		return github.PRRef{}, fmt.Errorf("resolve the repo for --pr %s: %w", raw, err)
	}
	return github.ParsePRRef(raw, &repo)
}

// startExtraLines renders the channel: and setup: lines (always), a stack: line
// for a Graphite stacked review, a pr: line for a pull-request review, and one
// organize: line per open request the daemon re-offered (the eager system
// organize plus any human AI-bar prompts left pending). An offer error degrades
// to offer=false with the error as the reason — start never fails on the setup
// check. The setup line's github key names what Claude's GitHub replies still
// need: the app setup command or the app's install URL.
func startExtraLines(started daemon.Started, offer bool, reason string, offerErr error) []string {
	if offerErr != nil {
		offer, reason = false, offerErr.Error()
	}
	setupFields := map[string]any{"offer": offer, "reason": reason}
	if started.GitHubSetup != "" {
		setupFields["github"] = started.GitHubSetup
	}
	setup, _ := json.Marshal(setupFields)
	lines := []string{"channel: " + started.ChannelState, "setup: " + string(setup)}
	if started.Stack != nil {
		stackJSON, _ := json.Marshal(started.Stack)
		lines = append(lines, "stack: "+string(stackJSON))
	}
	if started.PR != nil {
		lines = append(lines, "pr: "+started.PR.String())
	}
	for _, organize := range started.AIRequests {
		if len(organize) > 0 {
			lines = append(lines, "organize: "+string(organize))
		}
	}
	return lines
}
