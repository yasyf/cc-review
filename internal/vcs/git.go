package vcs

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

func gitRoot(ctx context.Context, cwd string) (string, error) {
	out, err := git(ctx, cwd, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// gitCapture snapshots the uncommitted working tree (tracked + staged +
// untracked, minus ignored) versus HEAD or the empty tree when there is no
// commit yet. It never mutates the caller's real index: all staging happens
// in a throwaway index via GIT_INDEX_FILE.
func gitCapture(ctx context.Context, cwd string) (Snapshot, error) {
	repoRoot, err := gitRoot(ctx, cwd)
	if err != nil {
		return Snapshot{}, err
	}

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
	// git writes <index>.lock during `add`; clean both so a cancelled git leaves nothing.
	defer func() {
		os.Remove(tmpPath)
		os.Remove(tmpPath + ".lock")
	}()

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
	files, err := parseFiles(patch)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		RepoRoot:  repoRoot,
		Branch:    branch,
		BaseRef:   base,
		PatchText: patch,
		Files:     files,
	}, nil
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
