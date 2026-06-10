// Package vcs snapshots a repository's pending changes as a single git-format
// patch, detecting whether the working copy is managed by git or jj. When a
// repository is colocated (both .jj and .git), jj wins.
package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type backend int

const (
	backendGit backend = iota
	backendJJ
)

// FileChange is one file's status within a snapshot.
type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status"` // A | M | D | R | C
}

// Snapshot is the result of snapshotting a working copy's pending changes.
type Snapshot struct {
	RepoRoot  string
	Branch    string // git: branch name, empty on detached HEAD; jj: nearest bookmark or change-id prefix
	BaseRef   string // git: "HEAD" or the empty-tree hash; jj: parent commit id
	PatchText string
	Files     []FileChange
}

// Root resolves the repository root containing cwd, for keying a review
// without taking a full snapshot.
func Root(ctx context.Context, cwd string) (string, error) {
	kind, dir, err := detect(cwd)
	if err != nil {
		return "", err
	}
	if kind == backendJJ {
		return dir, nil
	}
	return gitRoot(ctx, cwd)
}

// Capture snapshots cwd's pending changes as a patch against the backend's
// base revision: HEAD (or the empty tree) for git, the working-copy parent
// for jj.
func Capture(ctx context.Context, cwd string) (Snapshot, error) {
	kind, dir, err := detect(cwd)
	if err != nil {
		return Snapshot{}, err
	}
	if kind == backendJJ {
		return jjCapture(ctx, cwd, dir)
	}
	return gitCapture(ctx, cwd)
}

// detect walks upward from cwd without spawning a subprocess: Root sits on
// the daemon's 1 Hz poll path. A .git entry may be a file (worktrees), but
// .jj is only ever a directory.
func detect(cwd string) (backend, string, error) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return 0, "", fmt.Errorf("resolve %s: %w", cwd, err)
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".jj")); err == nil && fi.IsDir() {
			return backendJJ, dir, nil
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return backendGit, dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, "", errors.New("not inside a git or jj repository")
		}
		dir = parent
	}
}
