package thinstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/testhome"
)

var repo = github.Repo{Owner: "acme", Name: "widgets"}

type remote struct {
	url       string
	dir       string
	mergeBase string
	head      string
	orphan    string
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: test helper running git against a test-controlled temp repo with test-controlled args.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRemote(t *testing.T, allowAnySHA bool) remote {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	run(t, root, "init", "-q", "-b", "main", src)
	run(t, src, "config", "user.email", "t@example.com")
	run(t, src, "config", "user.name", "t")
	write := func(file, content string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		run(t, src, "add", file)
		run(t, src, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", file)
		return run(t, src, "rev-parse", "HEAD")
	}
	write("a.txt", "one\n")
	mergeBase := write("b.txt", "two\n")
	write("c.txt", "trunk moved on\n")
	run(t, src, "checkout", "-q", "-b", "feature", mergeBase)
	write("a.txt", "one\nfeature\n")
	head := write("d.txt", "new file\n")
	run(t, src, "checkout", "-q", "--orphan", "lost")
	orphan := write("z.txt", "unreachable\n")
	run(t, src, "checkout", "-q", "main")

	bare := filepath.Join(root, "remote.git")
	run(t, root, "clone", "-q", "--bare", src, bare)
	run(t, bare, "update-ref", "refs/pull/7/head", head)
	run(t, bare, "update-ref", "-d", "refs/heads/feature")
	run(t, bare, "update-ref", "-d", "refs/heads/lost")
	run(t, bare, "config", "uploadpack.allowFilter", "true")
	if allowAnySHA {
		run(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	}
	return remote{url: "file://" + bare, dir: bare, mergeBase: mergeBase, head: head, orphan: orphan}
}

func TestOpenClonesABloblessStore(t *testing.T) {
	home := testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()

	s, err := Open(ctx, repo, r.url)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".cc-review", "v1", "repos", "acme", "widgets.git"); s.Dir != want {
		t.Fatalf("Dir = %q, want %q", s.Dir, want)
	}
	for key, want := range map[string]string{
		"core.bare":                        "true",
		"remote.origin.promisor":           "true",
		"remote.origin.partialclonefilter": "blob:none",
		"feature.manyFiles":                "true",
		"pack.threads":                     "2",
	} {
		if got := run(t, s.Dir, "config", "--get", key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, err := Open(ctx, repo, r.url); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

func TestOpenRefusesAStoreThatLostItsFilter(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.url)
	if err != nil {
		t.Fatal(err)
	}
	run(t, s.Dir, "config", "--unset", "remote.origin.promisor")
	if _, err := Open(ctx, repo, r.url); err == nil || !strings.Contains(err.Error(), "remote.origin.promisor") {
		t.Fatalf("Open = %v, want a promisor mismatch", err)
	}
}

func TestFetchPRAndCommitDiffLazily(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.url)
	if err != nil {
		t.Fatal(err)
	}

	head, err := s.FetchPR(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if head != r.head {
		t.Fatalf("FetchPR = %s, want %s", head, r.head)
	}
	if err := s.FetchCommit(ctx, r.mergeBase); err != nil {
		t.Fatal(err)
	}
	present, err := s.has(ctx, r.mergeBase)
	if err != nil || !present {
		t.Fatalf("has(mergeBase) = %v, %v; want present", present, err)
	}
	if got := run(t, s.Dir, "diff", "--name-status", r.mergeBase, head); got != "M\ta.txt\nA\td.txt" {
		t.Fatalf("diff = %q", got)
	}
	if got := run(t, s.Dir, "show", head+":a.txt"); got != "one\nfeature" {
		t.Fatalf("a.txt at head = %q", got)
	}
}

func TestFetchCommitDeepensWhenTheServerRefusesShas(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, false)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.url)
	if err != nil {
		t.Fatal(err)
	}
	run(t, s.Dir, "config", "protocol.version", "0")
	if _, err := s.FetchPR(ctx, 7); err != nil {
		t.Fatal(err)
	}

	if err := s.FetchCommit(ctx, r.mergeBase); err != nil {
		t.Fatal(err)
	}
	present, err := s.has(ctx, r.mergeBase)
	if err != nil || !present {
		t.Fatalf("has(mergeBase) = %v, %v; want present", present, err)
	}

	err = s.FetchCommit(ctx, r.orphan)
	if err == nil || !strings.Contains(err.Error(), "not within 4096 commits") {
		t.Fatalf("FetchCommit(orphan) = %v, want the deepen cap", err)
	}
}
