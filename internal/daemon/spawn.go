package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/version"
)

// UpgradeTimeout bounds EnsureCurrent's worst case: an eviction's graceful
// shutdown, SIGKILL, and process-exit waits plus the new daemon's boot.
const UpgradeTimeout = 30 * time.Second

// EnsureCurrent returns once a daemon at this binary's version is reachable,
// spawning a detached `cc-review daemon` if none is. A reachable daemon at a
// different build version is replaced: the spawned daemon's listen() evicts the
// skewed holder. Used by every user-facing command, so a stale daemon dies on
// first contact with a newer binary. A flock around the spawn serializes
// simultaneous cold starts so only one process binds the socket.
func EnsureCurrent(timeout time.Duration) error {
	c := NewClient()
	if currentVersion(c) {
		return nil
	}
	deadline := time.Now().Add(timeout)
	return underStartLock(deadline, func() error {
		if currentVersion(c) { // a concurrent command already upgraded
			return nil
		}
		if err := spawnDaemon(); err != nil {
			return err
		}
		// Poll the version, not mere availability: the old daemon keeps
		// answering on the socket throughout its own eviction.
		return waitFor(deadline, func() bool { return currentVersion(c) },
			"daemon did not reach the current version in time")
	})
}

func currentVersion(c *Client) bool {
	resp, err := c.Health()
	return err == nil && resp.OK && resp.DaemonVersion == version.String()
}

// underStartLock runs fn while holding the exclusive start flock, waiting for
// it until deadline.
func underStartLock(deadline time.Time, fn func() error) error {
	if err := paths.EnsureLockDir(); err != nil {
		return err
	}
	lock, err := os.OpenFile(paths.StartLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open start lock: %w", err)
	}
	defer lock.Close()

	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("acquire start lock: %w", err)
		}
		if time.Now().After(deadline) {
			return errors.New("timed out acquiring daemon start lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	return fn()
}

// spawnDaemon starts a detached `cc-review daemon` (Setsid + Release) that
// outlives the CLI.
func spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

func waitFor(deadline time.Time, ok func() bool, timeoutMsg string) error {
	for time.Now().Before(deadline) {
		if ok() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New(timeoutMsg)
}
