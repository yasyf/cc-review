package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	dir := t.TempDir()
	gitInit(t, dir, "init", "-q", "-b", "main")
	return dir
}

func TestCaptureTrackedAndUntracked(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "kept.go", "package a\n")
	write(t, dir, "gone.go", "package a\nvar X int\n")
	gitInit(t, dir, "add", "-A")
	gitInit(t, dir, "commit", "-qm", "init")

	// Uncommitted: modify a tracked file, add an untracked file, delete a tracked file.
	write(t, dir, "kept.go", "package a\nfunc New() {}\n")
	write(t, dir, "fresh.go", "package a\n// brand new\n")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}

	snap, err := Capture(context.Background(), dir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.BaseRef != "HEAD" {
		t.Fatalf("base = %q, want HEAD", snap.BaseRef)
	}
	if snap.Branch != "main" {
		t.Fatalf("branch = %q, want main", snap.Branch)
	}
	for _, want := range []string{"fresh.go", "func New()", "gone.go"} {
		if !strings.Contains(snap.PatchText, want) {
			t.Fatalf("patch missing %q:\n%s", want, snap.PatchText)
		}
	}

	byPath := map[string]string{}
	for _, f := range snap.Files {
		byPath[f.Path] = f.Status
	}
	if byPath["fresh.go"] != "A" || byPath["kept.go"] != "M" || byPath["gone.go"] != "D" {
		t.Fatalf("file statuses = %+v, want fresh=A kept=M gone=D", byPath)
	}

	// The real index must be untouched: gone.go still tracked, fresh.go untracked.
	status := gitInit(t, dir, "status", "--porcelain")
	if !strings.Contains(status, "?? fresh.go") {
		t.Fatalf("fresh.go should still be untracked in the real index:\n%s", status)
	}
}

func TestCaptureCommitlessUsesEmptyTree(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "hello\n")

	snap, err := Capture(context.Background(), dir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.BaseRef != emptyTree {
		t.Fatalf("base = %q, want empty-tree %q", snap.BaseRef, emptyTree)
	}
	if !strings.Contains(snap.PatchText, "a.txt") || !strings.Contains(snap.PatchText, "hello") {
		t.Fatalf("commitless patch missing the new file:\n%s", snap.PatchText)
	}
	if len(snap.Files) != 1 || snap.Files[0].Status != "A" {
		t.Fatalf("files = %+v, want one added file", snap.Files)
	}
}

func TestCaptureDetachedHeadHasNoBranch(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "1\n")
	gitInit(t, dir, "add", "-A")
	gitInit(t, dir, "commit", "-qm", "c1")
	sha := strings.TrimSpace(gitInit(t, dir, "rev-parse", "HEAD"))
	gitInit(t, dir, "checkout", "-q", sha) // detach

	write(t, dir, "a.txt", "1\n2\n")
	snap, err := Capture(context.Background(), dir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.Branch != "" {
		t.Fatalf("branch = %q, want empty on detached HEAD", snap.Branch)
	}
	if snap.BaseRef != "HEAD" {
		t.Fatalf("base = %q, want HEAD", snap.BaseRef)
	}
}
