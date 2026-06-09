package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/yasyf/cc-review/internal/paths"
)

// EnsureRunning returns once the daemon is reachable, spawning a detached
// `cc-review daemon` if it is not. A flock around the spawn serializes
// simultaneous cold starts so only one process binds the socket; the spawn is
// detached (Setsid + Release) so it outlives the CLI. There is no version-skew
// eviction: a stale socket from a crashed daemon is cleared by the new daemon's
// os.Remove before it binds.
func EnsureRunning(timeout time.Duration) error {
	c := NewClient()
	if c.Available() {
		return nil
	}
	if err := paths.EnsureLockDir(); err != nil {
		return err
	}
	lock, err := os.OpenFile(paths.StartLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open start lock: %w", err)
	}
	defer lock.Close()

	deadline := time.Now().Add(timeout)
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

	// Another start may have spawned the daemon while we waited for the lock.
	if c.Available() {
		return nil
	}

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

	for time.Now().Before(deadline) {
		if c.Available() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("daemon did not become available in time")
}
