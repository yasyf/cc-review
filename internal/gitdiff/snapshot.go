// Package gitdiff snapshots a repository's uncommitted working tree (tracked +
// staged + untracked, minus ignored) as a single patch, versus HEAD or the empty
// tree when there is no commit yet. It never mutates the caller's real index: all
// staging happens in a throwaway index via GIT_INDEX_FILE.
package gitdiff

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// emptyTree is git's well-known hash of the empty tree, used as the diff base
// when the repository has no commits.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// FileChange is one file's status within a snapshot.
type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status"` // A | M | D | R | C | T
}

// Snapshot is the result of snapshotting a working tree.
type Snapshot struct {
	RepoRoot  string
	Branch    string // empty on a detached HEAD
	BaseRef   string // "HEAD" or the empty-tree hash
	PatchText string
	Files     []FileChange
}

// RepoRoot resolves the repository root containing cwd, for keying a review
// without taking a full snapshot.
func RepoRoot(ctx context.Context, cwd string) (string, error) {
	out, err := git(ctx, cwd, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// Capture snapshots cwd's uncommitted working tree as a patch against HEAD (or
// the empty tree if commitless).
func Capture(ctx context.Context, cwd string) (Snapshot, error) {
	repoRoot, err := git(ctx, cwd, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve repo root: %w", err)
	}
	repoRoot = strings.TrimSpace(repoRoot)

	// symbolic-ref -q exits non-zero with empty output on a detached HEAD; that
	// is a valid state, so a non-empty error here just means "no branch".
	branch, _ := git(ctx, cwd, nil, "symbolic-ref", "--short", "-q", "HEAD")
	branch = strings.TrimSpace(branch)

	base := emptyTree
	if _, err := git(ctx, cwd, nil, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		base = "HEAD"
	}

	tmpIndex, err := os.CreateTemp("", "cc-review-index-*")
	if err != nil {
		return Snapshot{}, fmt.Errorf("create temp index: %w", err)
	}
	tmpPath := tmpIndex.Name()
	tmpIndex.Close()
	os.Remove(tmpPath) // git wants to create it itself; we just reserved the name
	defer os.Remove(tmpPath)

	absIndex, err := filepath.Abs(tmpPath)
	if err != nil {
		return Snapshot{}, err
	}
	env := []string{"GIT_INDEX_FILE=" + absIndex}

	// Stage the entire working tree (tracked changes, deletions, and untracked
	// non-ignored files) into the throwaway index, then diff it against the base.
	if _, err := git(ctx, cwd, env, "add", "-A"); err != nil {
		return Snapshot{}, fmt.Errorf("stage working tree: %w", err)
	}
	patch, err := git(ctx, cwd, env, "diff", "--cached", "--no-color", base)
	if err != nil {
		return Snapshot{}, fmt.Errorf("diff working tree: %w", err)
	}
	nameStatus, err := git(ctx, cwd, env, "diff", "--cached", "--name-status", "-z", base)
	if err != nil {
		return Snapshot{}, fmt.Errorf("diff name-status: %w", err)
	}

	return Snapshot{
		RepoRoot:  repoRoot,
		Branch:    branch,
		BaseRef:   base,
		PatchText: patch,
		Files:     parseNameStatusZ(nameStatus),
	}, nil
}

// parseNameStatusZ parses `git diff --name-status -z` output. Records are
// NUL-separated; rename/copy records carry two paths (status, old, new).
func parseNameStatusZ(s string) []FileChange {
	fields := strings.Split(s, "\x00")
	var out []FileChange
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		if status == "" {
			continue
		}
		code := status[:1]
		if code == "R" || code == "C" {
			if i+2 >= len(fields) {
				break
			}
			out = append(out, FileChange{Status: code, OldPath: fields[i+1], Path: fields[i+2]})
			i += 2
			continue
		}
		if i+1 >= len(fields) {
			break
		}
		out = append(out, FileChange{Status: code, Path: fields[i+1]})
		i++
	}
	return out
}

func git(ctx context.Context, cwd string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
