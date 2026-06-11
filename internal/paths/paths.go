// Package paths owns the canonical ~/.cc-review state-directory layout: the
// sqlite db, the daemon's unix socket, the http handshake file, per-review
// snapshot/feedback files, and the lazy-start lock dir. The directory is always
// ~/.cc-review and is never relocated by an environment variable, so cc-review's
// own state stays stable regardless of CLAUDE_CONFIG_DIR.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func mustHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("resolve home dir: %v", err))
	}
	return h
}

// StateDir is cc-review's private state directory (~/.cc-review).
func StateDir() string { return filepath.Join(mustHome(), ".cc-review") }

// DBPath is the sqlite database path.
func DBPath() string { return filepath.Join(StateDir(), "review.db") }

// SocketPath is the daemon control-plane unix socket.
func SocketPath() string { return filepath.Join(StateDir(), "daemon.sock") }

// LogPath is the daemon log path.
func LogPath() string { return filepath.Join(StateDir(), "daemon.log") }

// HTTPInfoPath is where the daemon publishes its ephemeral port so the CLI
// and the SSE consumers can reach the HTTP plane.
func HTTPInfoPath() string { return filepath.Join(StateDir(), "http.json") }

// LockDir holds the lazy-start flock file.
func LockDir() string { return filepath.Join(StateDir(), "locks") }

// StartLockPath is the flock file serializing lazy daemon starts.
func StartLockPath() string { return filepath.Join(LockDir(), "start.lock") }

// ChannelSetupMarker records that the one-time approved-channels offer was made,
// so /cc-review:start asks at most once. Its presence is the gate; the JSON body
// (status done|declined) is provenance only.
func ChannelSetupMarker() string { return filepath.Join(StateDir(), "channels-setup.json") }

// ReviewsDir is the parent of all per-review on-disk artifacts.
func ReviewsDir() string { return filepath.Join(StateDir(), "reviews") }

// ReviewDir is the on-disk artifact directory for a single review.
func ReviewDir(reviewID string) string { return filepath.Join(ReviewsDir(), reviewID) }

// SnapshotPath is the patch file for version v of a review.
func SnapshotPath(reviewID string, version int) string {
	return filepath.Join(ReviewDir(reviewID), fmt.Sprintf("snap_%d.patch", version))
}

// FeedbackPath is the frozen feedback JSON for version v of a review.
func FeedbackPath(reviewID string, version int) string {
	return filepath.Join(ReviewDir(reviewID), fmt.Sprintf("feedback_%d.json", version))
}

// ConsumerCursorPath is where a Claude-side stream consumer (e.g. "watch" or
// "channel") persists its last-delivered event seq, so a restart resumes without
// re-delivering. Each consumer keeps its own cursor, so they never contend.
func ConsumerCursorPath(reviewID, consumer string) string {
	return filepath.Join(ReviewDir(reviewID), consumer+".cursor")
}

// TurnsDir is the parent of all per-repo turn-snapshot scratch dirs.
func TurnsDir() string { return filepath.Join(StateDir(), "turns") }

// RepoTurnsDir is the turn-snapshot scratch dir for a single repo, keyed by a
// hash of its root so absolute paths never leak into directory names.
func RepoTurnsDir(repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	return filepath.Join(TurnsDir(), hex.EncodeToString(sum[:8]))
}

// EnsureStateDir creates ~/.cc-review (0700) if missing.
func EnsureStateDir() error {
	return os.MkdirAll(StateDir(), 0o700)
}

// EnsureLockDir creates the lock dir (0700) if missing.
func EnsureLockDir() error {
	return os.MkdirAll(LockDir(), 0o700)
}

// EnsureReviewDir creates a review's artifact dir (0700) if missing.
func EnsureReviewDir(reviewID string) error {
	return os.MkdirAll(ReviewDir(reviewID), 0o700)
}

// EnsureRepoTurnsDir creates a repo's turn-snapshot scratch dir (0700) if
// missing and returns it.
func EnsureRepoTurnsDir(repoRoot string) (string, error) {
	dir := RepoTurnsDir(repoRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create turns dir: %w", err)
	}
	return dir, nil
}
