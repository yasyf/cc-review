// Package thinstoretest builds local bare remotes that serve pull request
// refs the way GitHub does, for tests of internal/thinstore and its callers.
package thinstoretest

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var identity = []string{
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_AUTHOR_DATE=2026-01-02T03:04:05Z",
	"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z",
}

// Remote is a bare repository served over file://. Its HEAD is main, which
// SetBranch must create before a store clones it.
type Remote struct {
	URL string
	Dir string
}

// NewRemote creates an empty bare remote that serves partial clones. With
// allowAnySHA it also answers wants by sha under protocol v0.
func NewRemote(t testing.TB, allowAnySHA bool) *Remote {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	git(t, filepath.Dir(dir), nil, "init", "-q", "--bare", "-b", "main", dir)
	git(t, dir, nil, "config", "uploadpack.allowFilter", "true")
	if allowAnySHA {
		git(t, dir, nil, "config", "uploadpack.allowAnySHA1InWant", "true")
	}
	return &Remote{URL: "file://" + dir, Dir: dir}
}

// Commit writes a commit whose tree is parent's with files laid over it and
// returns its sha. An empty parent makes a root commit.
func (r *Remote) Commit(t testing.TB, parent string, files map[string]string) string {
	t.Helper()
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	if parent != "" {
		git(t, r.Dir, index, "read-tree", parent)
	}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		blob := gitStdin(t, r.Dir, files[path], "hash-object", "-w", "--stdin")
		git(t, r.Dir, index, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	tree := git(t, r.Dir, index, "write-tree")
	args := []string{"commit-tree", tree, "-m", "commit " + strings.Join(slices.Sorted(maps.Keys(files)), " ")}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return git(t, r.Dir, identity, args...)
}

// SetBranch points refs/heads/<name> at sha.
func (r *Remote) SetBranch(t testing.TB, name, sha string) {
	t.Helper()
	git(t, r.Dir, nil, "update-ref", "refs/heads/"+name, sha)
}

// SetPR points refs/pull/<number>/head at sha.
func (r *Remote) SetPR(t testing.TB, number int, sha string) {
	t.Helper()
	git(t, r.Dir, nil, "update-ref", "refs/pull/"+strconv.Itoa(number)+"/head", sha)
}

func git(t testing.TB, dir string, env []string, args ...string) string {
	t.Helper()
	return run(t, dir, env, "", args...)
}

func gitStdin(t testing.TB, dir, stdin string, args ...string) string {
	t.Helper()
	return run(t, dir, nil, stdin, args...)
}

func run(t testing.TB, dir string, env []string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: test helper running git against a test-controlled temp repo with test-controlled args.
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}
