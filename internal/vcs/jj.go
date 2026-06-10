package vcs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// jjCapture diffs the working-copy commit against its parents; the implicit
// working-copy snapshot that jj takes on every invocation is exactly the
// "uncommitted changes" we want, so no --ignore-working-copy.
func jjCapture(ctx context.Context, cwd, repoRoot string) (Snapshot, error) {
	branch, err := jjBranch(ctx, cwd)
	if err != nil {
		return Snapshot{}, err
	}

	baseOut, err := jj(ctx, cwd, "log", "--no-graph", "-r", "@-", "-T", `commit_id.shortest(12) ++ "\n"`)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve base commit: %w", err)
	}
	// A merge working copy has multiple parents; the first is fine as the base.
	base, _, _ := strings.Cut(baseOut, "\n")

	patch, err := jj(ctx, cwd, "diff", "-r", "@", "--git")
	if err != nil {
		return Snapshot{}, fmt.Errorf("diff working copy: %w", err)
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

// jjBranch resolves the nearest ancestor bookmark, falling back to the
// working-copy change id — the jj-idiomatic identity — when no bookmark is
// in the ancestry.
func jjBranch(ctx context.Context, cwd string) (string, error) {
	out, err := jj(ctx, cwd, "log", "--no-graph",
		"-r", "latest(::@ & bookmarks())",
		"-T", `local_bookmarks.map(|b| b.name()).join(" ")`)
	if err != nil {
		return "", fmt.Errorf("resolve bookmark: %w", err)
	}
	// A revset with no matching revisions exits 0 with empty output, so empty
	// means "no bookmark in the ancestry", not failure.
	if names := strings.Fields(out); len(names) > 0 {
		return names[0], nil
	}
	out, err = jj(ctx, cwd, "log", "--no-graph", "-r", "@", "-T", "change_id.shortest(8)")
	if err != nil {
		return "", fmt.Errorf("resolve change id: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func jj(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "jj", append([]string{"--color=never", "--no-pager"}, args...)...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("jj %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
