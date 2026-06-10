package channelsetup

import (
	"os"
	"strings"

	"github.com/yasyf/cc-review/internal/procs"
)

// devChannelFlag is the launch flag that triggers Claude's "Loading development
// channels" confirmation. A session carrying it is exactly the population the
// approved-channels offer targets.
const devChannelFlag = "dangerously-load-development-channels"

// LaunchedWithDevChannels reports whether this process descends from a Claude
// launched with --dangerously-load-development-channels.
func LaunchedWithDevChannels() (bool, error) {
	return procs.FindAncestor(os.Getpid(), hasDevChannelFlag) != 0, nil
}

func hasDevChannelFlag(argv []string) bool {
	return strings.Contains(strings.Join(argv, " "), devChannelFlag)
}
