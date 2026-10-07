package thinstore

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/testhome"
	"github.com/yasyf/cc-review/internal/thinstore/thinstoretest"
)

var repo = github.Repo{Owner: "acme", Name: "widgets"}

type fixture struct {
	*thinstoretest.Remote
	mergeBase string
	head      string
	orphan    string
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output() //nolint:gosec // G204: test helper running git against a test-controlled temp repo with test-controlled args.
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func newRemote(t *testing.T, allowAnySHA bool) fixture {
	t.Helper()
	r := thinstoretest.NewRemote(t, allowAnySHA)
	root := r.Commit(t, "", map[string]string{"a.txt": "one\n"})
	mergeBase := r.Commit(t, root, map[string]string{"b.txt": "two\n"})
	r.SetBranch(t, "main", r.Commit(t, mergeBase, map[string]string{"c.txt": "trunk moved on\n"}))
	feature := r.Commit(t, mergeBase, map[string]string{"a.txt": "one\nfeature\n"})
	head := r.Commit(t, feature, map[string]string{"d.txt": "new file\n"})
	r.SetPR(t, 7, head)
	orphan := r.Commit(t, "", map[string]string{"z.txt": "unreachable\n"})
	return fixture{Remote: r, mergeBase: mergeBase, head: head, orphan: orphan}
}

func TestOpenClonesABloblessStore(t *testing.T) {
	home := testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()

	s, err := Open(ctx, repo, r.URL)
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
	if _, err := Open(ctx, repo, r.URL); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

func TestOpenRefusesAStoreThatLostItsFilter(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.URL)
	if err != nil {
		t.Fatal(err)
	}
	run(t, s.Dir, "config", "--unset", "remote.origin.promisor")
	if _, err := Open(ctx, repo, r.URL); err == nil || !strings.Contains(err.Error(), "remote.origin.promisor") {
		t.Fatalf("Open = %v, want a promisor mismatch", err)
	}
}

func TestFetchPRAndCommitDiffLazily(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.URL)
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
	s, err := Open(ctx, repo, r.URL)
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
