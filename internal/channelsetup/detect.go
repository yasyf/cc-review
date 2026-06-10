package channelsetup

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// devChannelFlag is the launch flag that triggers Claude's "Loading development
// channels" confirmation. A session carrying it is exactly the population the
// approved-channels offer targets.
const devChannelFlag = "dangerously-load-development-channels"

type procEntry struct {
	ppid int
	cmd  string
}

// LaunchedWithDevChannels reports whether this process descends from a Claude
// launched with --dangerously-load-development-channels.
func LaunchedWithDevChannels() (bool, error) {
	table, err := readProcTable()
	if err != nil {
		return false, err
	}
	return ancestryContains(table, os.Getpid(), devChannelFlag), nil
}

func readProcTable() (map[int]procEntry, error) {
	out, err := exec.Command("ps", "-ax", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	table := map[int]procEntry{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, errPID := strconv.Atoi(fields[0])
		ppid, errPPID := strconv.Atoi(fields[1])
		if errPID != nil || errPPID != nil {
			continue
		}
		table[pid] = procEntry{ppid: ppid, cmd: strings.Join(fields[2:], " ")}
	}
	return table, nil
}

func ancestryContains(table map[int]procEntry, start int, needle string) bool {
	seen := map[int]bool{}
	for pid := start; pid > 1 && !seen[pid]; {
		seen[pid] = true
		e, ok := table[pid]
		if !ok {
			break
		}
		if strings.Contains(e.cmd, needle) {
			return true
		}
		pid = e.ppid
	}
	return false
}
