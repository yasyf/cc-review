package vcs

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireJJ(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not installed")
	}
	t.Setenv("JJ_USER", "t")
	t.Setenv("JJ_EMAIL", "t@t")
}

func jjRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("jj", append([]string{"--color=never", "--no-pager"}, args...)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("jj %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

func newJJRepo(t *testing.T, colocate bool) string {
	dir := t.TempDir()
	args := []string{"git", "init"}
	if colocate {
		args = append(args, "--colocate")
	}
	jjRun(t, dir, append(args, ".")...)
	return dir
}

func TestJJCaptureColocatedWithBookmark(t *testing.T) {
	requireJJ(t)
	dir := newJJRepo(t, true)
	write(t, dir, "a.txt", "hello\n")
	jjRun(t, dir, "bookmark", "create", "somebranch", "-r", "@")

	snap, err := Capture(context.Background(), dir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.Branch != "somebranch" {
		t.Fatalf("branch = %q, want somebranch", snap.Branch)
	}
	if snap.RepoRoot != dir {
		t.Fatalf("repo root = %q, want %q", snap.RepoRoot, dir)
	}
	for _, want := range []string{"a.txt", "hello"} {
		if !strings.Contains(snap.PatchText, want) {
			t.Fatalf("patch missing %q:\n%s", want, snap.PatchText)
		}
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != "a.txt" || snap.Files[0].Status != "A" {
		t.Fatalf("files = %+v, want a.txt added", snap.Files)
	}
}

func TestJJCaptureNoBookmarkUsesChangeID(t *testing.T) {
	requireJJ(t)
	dir := newJJRepo(t, true)
	write(t, dir, "a.txt", "hello\n")

	snap, err := Capture(context.Background(), dir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(snap.Branch) != 8 {
		t.Fatalf("branch = %q, want an 8-char change-id prefix", snap.Branch)
	}
	want := strings.TrimSpace(jjRun(t, dir, "log", "--no-graph", "-r", "@", "-T", "change_id.shortest(8)"))
	if snap.Branch != want {
		t.Fatalf("branch = %q, want change id %q", snap.Branch, want)
	}
}

func TestJJCapturePureRepo(t *testing.T) {
	requireJJ(t)
	dir := newJJRepo(t, false)
	write(t, dir, "a.txt", "hello\n")

	snap, err := Capture(context.Background(), dir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.RepoRoot != dir {
		t.Fatalf("repo root = %q, want %q", snap.RepoRoot, dir)
	}
	if len(snap.BaseRef) != 12 {
		t.Fatalf("base = %q, want a 12-char commit id", snap.BaseRef)
	}
	want := strings.TrimSpace(jjRun(t, dir, "log", "--no-graph", "-r", "@-", "-T", "commit_id.shortest(12)"))
	if snap.BaseRef != want {
		t.Fatalf("base = %q, want parent commit %q", snap.BaseRef, want)
	}
	if !strings.Contains(snap.PatchText, "hello") {
		t.Fatalf("patch missing the new file:\n%s", snap.PatchText)
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != "a.txt" || snap.Files[0].Status != "A" {
		t.Fatalf("files = %+v, want a.txt added", snap.Files)
	}
}

func TestJJCaptureFileStatuses(t *testing.T) {
	requireJJ(t)
	dir := newJJRepo(t, false)
	write(t, dir, "kept.go", "package a\n")
	write(t, dir, "gone.go", "package a\nvar X int\n")
	jjRun(t, dir, "commit", "-m", "init")

	// fresh.go must not resemble gone.go, or jj's rename detection pairs them
	// into an R instead of A + D.
	write(t, dir, "kept.go", "package a\nfunc New() {}\n")
	write(t, dir, "fresh.go", "while the moon spins backwards\nthe lighthouse hums in C minor\n")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}

	snap, err := Capture(context.Background(), dir)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	byPath := map[string]string{}
	for _, f := range snap.Files {
		byPath[f.Path] = f.Status
	}
	if byPath["fresh.go"] != "A" || byPath["kept.go"] != "M" || byPath["gone.go"] != "D" {
		t.Fatalf("file statuses = %+v, want fresh=A kept=M gone=D", byPath)
	}
}
