package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yasyf/cc-review/internal/channelsetup"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/version"
)

// newSetupChannelsCmd is the hidden command behind /cc-review:start's one-time
// offer to approve cc-review's channel. --check reports whether to offer (not
// yet approved, not yet asked); --apply writes the approved-channels config
// through an admin prompt; --decline records a "no" so the offer never
// repeats.
func newSetupChannelsCmd() *cobra.Command {
	var check, apply, decline bool
	cmd := &cobra.Command{
		Use:    "setup-channels",
		Hidden: true,
		Short:  "Make cc-review an approved Claude channel (silences the dev-channels warning)",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case apply:
				return runChannelsApply(cmd.OutOrStdout())
			case decline:
				return writeChannelMarker("declined")
			default:
				return runChannelsCheck(cmd.OutOrStdout())
			}
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "print {offer,reason} JSON for the first-run offer (default)")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the approved-channels config (prompts for admin)")
	cmd.Flags().BoolVar(&decline, "decline", false, "record that the offer was declined")
	return cmd
}

func runChannelsCheck(out io.Writer) error {
	offer, reason, err := channelsOffer()
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"offer": offer, "reason": reason})
}

func channelsOffer() (bool, string, error) {
	if _, err := os.Stat(paths.ChannelSetupMarker()); err == nil {
		return false, "already offered", nil
	} else if !os.IsNotExist(err) {
		return false, "", fmt.Errorf("stat channel marker: %w", err)
	}
	managed, err := readFileOrEmpty(channelsetup.ManagedSettingsPath())
	if err != nil {
		return false, "", err
	}
	approved, err := channelsetup.ManagedHasEntry(managed)
	if err != nil {
		return false, "", err
	}
	if approved {
		return false, "already approved", nil
	}
	return true, "channel not yet approved", nil
}

func runChannelsApply(out io.Writer) error {
	managed, err := readFileOrEmpty(channelsetup.ManagedSettingsPath())
	if err != nil {
		return err
	}
	mergedManaged, err := channelsetup.MergeManaged(managed)
	if err != nil {
		return err
	}
	if err := channelsetup.ApplyManagedViaAdmin(mergedManaged); err != nil {
		return err
	}
	if err := applyUserChannels(); err != nil {
		return err
	}
	if err := writeChannelMarker("done"); err != nil {
		return err
	}
	fmt.Fprintln(out, "cc-review is now an approved channel.")
	fmt.Fprintf(out, "Launch with `--channels %s` (replacing `--dangerously-load-development-channels %s` if you used it) — same channel, no warning.\n", channelsetup.ChannelID, channelsetup.ChannelID)
	return nil
}

func applyUserChannels() error {
	path := channelsetup.UserSettingsPath()
	existing, err := readFileOrEmpty(path)
	if err != nil {
		return err
	}
	merged, err := channelsetup.MergeUserSettings(existing)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		return fmt.Errorf("write user settings: %w", err)
	}
	return nil
}

func writeChannelMarker(status string) error {
	if err := paths.EnsureStateDir(); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"status": status, "version": version.String()})
	if err != nil {
		return err
	}
	return os.WriteFile(paths.ChannelSetupMarker(), body, 0o644)
}

func readFileOrEmpty(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}
