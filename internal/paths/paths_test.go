package paths

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRepoTurnsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name     string
		repoRoot string
	}{
		{name: "simple root", repoRoot: "/Users/alice/code/project"},
		{name: "root with spaces", repoRoot: "/Users/alice/my project"},
	}
	hashed := regexp.MustCompile(`^[0-9a-f]{16}$`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepoTurnsDir(tt.repoRoot)
			if again := RepoTurnsDir(tt.repoRoot); again != got {
				t.Fatalf("RepoTurnsDir not deterministic: %q vs %q", got, again)
			}
			if dir := filepath.Dir(got); dir != TurnsDir() {
				t.Fatalf("parent = %q, want %q", dir, TurnsDir())
			}
			if base := filepath.Base(got); !hashed.MatchString(base) {
				t.Fatalf("base = %q, want 16 hex chars", base)
			}
		})
	}

	if RepoTurnsDir("/repo/a") == RepoTurnsDir("/repo/b") {
		t.Fatal("distinct repo roots mapped to the same turns dir")
	}
}

func TestEnsureRepoTurnsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir, err := EnsureRepoTurnsDir("/repo/a")
	if err != nil {
		t.Fatalf("EnsureRepoTurnsDir: %v", err)
	}
	if dir != RepoTurnsDir("/repo/a") {
		t.Fatalf("returned %q, want %q", dir, RepoTurnsDir("/repo/a"))
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("perm = %o, want 700", perm)
	}

	if _, err := EnsureRepoTurnsDir("/repo/a"); err != nil {
		t.Fatalf("EnsureRepoTurnsDir on existing dir: %v", err)
	}
}
