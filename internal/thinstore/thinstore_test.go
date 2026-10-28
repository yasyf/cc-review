package thinstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestFetchPRsAndCommitsDiffLazily(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.URL)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.FetchPRs(ctx, github.PullRequest{Number: 7, HeadRefOid: r.head}); err != nil {
		t.Fatal(err)
	}
	head := run(t, s.Dir, "rev-parse", prRefPrefix+"7")
	if head != r.head {
		t.Fatalf("refs/cc-review/pr/7 = %s, want %s", head, r.head)
	}
	if err := s.FetchCommits(ctx, r.mergeBase); err != nil {
		t.Fatal(err)
	}
	if absent, err := s.missing(ctx, r.mergeBase, r.head); err != nil || len(absent) != 0 {
		t.Fatalf("missing(mergeBase, head) = %v, %v; want none", absent, err)
	}
	if got := run(t, s.Dir, "diff", "--name-status", r.mergeBase, head); got != "M\ta.txt\nA\td.txt" {
		t.Fatalf("diff = %q", got)
	}
	if got := run(t, s.Dir, "show", head+":a.txt"); got != "one\nfeature" {
		t.Fatalf("a.txt at head = %q", got)
	}
}

func TestFetchCommitsDeepensWhenTheServerRefusesShas(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, false)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.URL)
	if err != nil {
		t.Fatal(err)
	}
	run(t, s.Dir, "config", "protocol.version", "0")
	if err := s.FetchPRs(ctx, github.PullRequest{Number: 7, HeadRefOid: r.head}); err != nil {
		t.Fatal(err)
	}

	if err := s.FetchCommits(ctx, r.mergeBase); err != nil {
		t.Fatal(err)
	}
	if absent, err := s.missing(ctx, r.mergeBase, r.head); err != nil || len(absent) != 0 {
		t.Fatalf("missing(mergeBase, head) = %v, %v; want none", absent, err)
	}

	err = s.FetchCommits(ctx, r.orphan)
	if err == nil || !strings.Contains(err.Error(), "not within 4096 commits") {
		t.Fatalf("FetchCommits(orphan) = %v, want the deepen cap", err)
	}
}

func TestFetchPRsPointsAPresentHeadWithoutFetching(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FetchPRs(ctx, github.PullRequest{Number: 7, HeadRefOid: r.head}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(r.Dir); err != nil {
		t.Fatal(err)
	}
	if err := s.FetchPRs(ctx, github.PullRequest{Number: 8, HeadRefOid: r.head}); err != nil {
		t.Fatalf("FetchPRs of a head the store holds, with the remote gone = %v", err)
	}
	if head := run(t, s.Dir, "rev-parse", prRefPrefix+"8"); head != r.head {
		t.Fatalf("refs/cc-review/pr/8 = %s, want %s", head, r.head)
	}
}

func TestFetchPRClearsAShallowLockAKilledFetchLeft(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.URL)
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(s.Dir, "shallow.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-staleLockAge - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.FetchPRs(ctx, github.PullRequest{Number: 7, HeadRefOid: r.head}); err != nil {
		t.Fatalf("FetchPRs with a stale shallow.lock = %v", err)
	}
	if head := run(t, s.Dir, "rev-parse", prRefPrefix+"7"); head != r.head {
		t.Fatalf("refs/cc-review/pr/7 = %s, want %s", head, r.head)
	}
}

func TestMissingNamesOnlyAbsentCommits(t *testing.T) {
	testhome.Temp(t)
	r := newRemote(t, true)
	ctx := context.Background()
	s, err := Open(ctx, repo, r.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FetchPRs(ctx, github.PullRequest{Number: 7, HeadRefOid: r.head}); err != nil {
		t.Fatal(err)
	}
	absent, err := s.missing(ctx, r.orphan, r.head, r.mergeBase)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{r.orphan, r.mergeBase}; !slices.Equal(absent, want) {
		t.Fatalf("missing = %v, want %v", absent, want)
	}
}
