package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/github/githubtest"
	"github.com/yasyf/cc-review/internal/store"
)

var prRepo = github.Repo{Owner: "acme", Name: "widgets"}

// prRemote serves a two-PR stack the way GitHub does: #1 feat-a on main and
// #2 feat-b on feat-a, reachable only through refs/pull/N/head.
type prRemote struct {
	src   string
	bare  string
	trunk string
	headA string
	headB string
}

func newPRRemote(t *testing.T) prRemote {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	gitRun(t, root, "init", "-q", "-b", "main", src)
	r := prRemote{src: src, bare: filepath.Join(root, "remote.git")}
	r.trunk = r.commit(t, "base.go", "package p\n")
	gitRun(t, src, "checkout", "-qb", "feat-a")
	r.headA = r.commit(t, "a.go", "package a\n\nfunc A() {}\n")
	gitRun(t, src, "checkout", "-qb", "feat-b")
	r.headB = r.commit(t, "b.go", "package b\n\nfunc B() {}\n")
	gitRun(t, root, "clone", "-q", "--bare", src, r.bare)
	gitRun(t, r.bare, "update-ref", "-d", "refs/heads/feat-a")
	gitRun(t, r.bare, "update-ref", "-d", "refs/heads/feat-b")
	gitRun(t, r.bare, "config", "uploadpack.allowFilter", "true")
	gitRun(t, r.bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	r.publish(t, 1, "feat-a")
	r.publish(t, 2, "feat-b")
	return r
}

func (r prRemote) url() string { return "file://" + r.bare }

func (r prRemote) commit(t *testing.T, file, content string) string {
	t.Helper()
	writeFile(t, r.src, file, content)
	gitRun(t, r.src, "add", "-A")
	gitRun(t, r.src, "-c", "core.hooksPath=/dev/null", "commit", "-qm", file)
	return strings.TrimSpace(gitRun(t, r.src, "rev-parse", "HEAD"))
}

func (r prRemote) publish(t *testing.T, number int, branch string) {
	t.Helper()
	gitRun(t, r.bare, "fetch", "-q", r.src, fmt.Sprintf("+refs/heads/%s:refs/pull/%d/head", branch, number))
}

// prStack points s at a fake GitHub serving remote's stack as #1 by alice (the
// viewer) and #2 by bob.
func prStack(t *testing.T, s *Server, remote prRemote) *githubtest.Server {
	t.Helper()
	gh := githubtest.New(t)
	gh.Login("token", "alice")
	gh.SetDefaultBranch(prRepo, "main")
	gh.AddPR(prRepo, github.PullRequest{Number: 1, Title: "A", AuthorLogin: "alice", HeadRefName: "feat-a", HeadRefOid: remote.headA, BaseRefName: "main"})
	gh.AddPR(prRepo, github.PullRequest{Number: 2, Title: "B", AuthorLogin: "bob", HeadRefName: "feat-b", HeadRefOid: remote.headB, BaseRefName: "feat-a"})
	gh.SetMergeBase(prRepo, "main", remote.headA, remote.trunk)
	gh.SetMergeBase(prRepo, remote.headA, remote.headB, remote.headA)
	s.rv.gh = gh.Client("token")
	s.rv.cloneURL = func(repo github.Repo) string {
		if repo != prRepo {
			t.Fatalf("clone url for %s, want %s", repo, prRepo)
		}
		return remote.url()
	}
	return gh
}

func prStart(ctx context.Context, t *testing.T, s *Server, cwd string, number int) Response {
	t.Helper()
	resp := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: cwd, PR: &github.PRRef{Repo: prRepo, Number: number}})
	if !resp.OK {
		t.Fatalf("pr start: %s", resp.Error)
	}
	return resp
}

type sectionShape struct {
	Branch, ParentBranch, BaseRef, HeadRef, PRNodeID string
	PRNumber                                         int
	Pending                                          bool
	Paths                                            string
}

func shapes(t *testing.T, sections []store.Section) []sectionShape {
	t.Helper()
	out := make([]sectionShape, len(sections))
	for i, sec := range sections {
		patch, err := os.ReadFile(sec.PatchPath)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for line := range strings.SplitSeq(string(patch), "\n") {
			if p, ok := strings.CutPrefix(line, "+++ b/"); ok {
				paths = append(paths, p)
			}
		}
		out[i] = sectionShape{
			Branch: sec.Branch, ParentBranch: sec.ParentBranch, BaseRef: sec.BaseRef, HeadRef: sec.HeadRef,
			PRNodeID: sec.PRNodeID, PRNumber: sec.PRNumber, Pending: sec.Pending, Paths: strings.Join(paths, ","),
		}
	}
	return out
}

func TestStartPRCapturesOneSectionPerStackedPR(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	remote := newPRRemote(t)
	prStack(t, s, remote)

	resp := prStart(ctx, t, s, repo, 2)
	if want := (&PRInfo{Repo: "acme/widgets", Number: 2, Stack: []int{1, 2}}); !reflect.DeepEqual(resp.PR, want) {
		t.Fatalf("PR = %+v, want %+v", resp.PR, want)
	}
	if resp.Stack != nil || resp.Version != 1 || resp.Resumed {
		t.Fatalf("stack=%+v version=%d resumed=%v, want no stack line on a fresh version 1", resp.Stack, resp.Version, resp.Resumed)
	}
	if resp.GitHubSetup != "cc-review github setup" {
		t.Fatalf("GitHubSetup = %q, want the app setup command", resp.GitHubSetup)
	}
	want := []sectionShape{
		{Branch: "feat-a", ParentBranch: "main", BaseRef: remote.trunk, HeadRef: remote.headA, PRNodeID: "PR_acme/widgets_1", PRNumber: 1, Paths: "a.go"},
		{Branch: "feat-b", ParentBranch: "feat-a", BaseRef: remote.headA, HeadRef: remote.headB, PRNodeID: "PR_acme/widgets_2", PRNumber: 2, Paths: "b.go"},
	}
	if got := shapes(t, s.latestSections(ctx, t, resp.ReviewID)); !reflect.DeepEqual(got, want) {
		t.Fatalf("sections = %+v\nwant %+v", got, want)
	}
	meta, ok, err := s.store.GetReviewMeta(ctx, resp.ReviewID)
	if err != nil || !ok {
		t.Fatalf("review meta: ok=%v err=%v", ok, err)
	}
	if meta.Kind != store.ReviewKindPR || meta.Repo != "acme/widgets" || meta.PRNumber != 2 || !meta.Stack {
		t.Fatalf("meta = %+v, want a pr review of acme/widgets#2", meta)
	}
	prs, err := s.store.PullRequests(ctx, resp.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	authored := make([]bool, 0, len(prs))
	for _, pr := range prs {
		authored = append(authored, pr.ViewerIsAuthor)
	}
	if len(prs) != 2 || prs[0].HeadSHA != remote.headA || prs[1].HeadSHA != remote.headB || !reflect.DeepEqual(authored, []bool{true, false}) {
		t.Fatalf("pull requests = %+v", prs)
	}
	if n := countEvents(t, s, resp.ReviewID, store.EventPRUpdated); n != 2 {
		t.Fatalf("pr.updated events = %d, want 2", n)
	}
	if got := s.openedPRReviews(); !reflect.DeepEqual(got, []string{resp.ReviewID}) {
		t.Fatalf("opened PR reviews = %v, want [%s]", got, resp.ReviewID)
	}
}

func TestStartPRResumeAfterAPushCreatesANewVersion(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	remote := newPRRemote(t)
	gh := prStack(t, s, remote)
	first := prStart(ctx, t, s, repo, 2)

	gitRun(t, remote.src, "checkout", "-q", "feat-b")
	pushed := remote.commit(t, "c.go", "package c\n")
	remote.publish(t, 2, "feat-b")
	gh.UpdatePR(prRepo, 2, func(pr *github.PullRequest) { pr.HeadRefOid = pushed })
	gh.SetMergeBase(prRepo, remote.headA, pushed, remote.headA)

	second := prStart(ctx, t, s, repo, 2)
	if second.ReviewID != first.ReviewID || second.Version != 2 || !second.Resumed {
		t.Fatalf("second start = review %s v%d resumed=%v, want %s v2 resumed", second.ReviewID, second.Version, second.Resumed, first.ReviewID)
	}
	got := shapes(t, s.latestSections(ctx, t, second.ReviewID))
	if len(got) != 2 || got[1].HeadRef != pushed || got[1].Paths != "b.go,c.go" || got[0].HeadRef != remote.headA {
		t.Fatalf("sections = %+v, want feat-b at the pushed head", got)
	}
	if n := countEvents(t, s, second.ReviewID, store.EventPRUpdated); n != 3 {
		t.Fatalf("pr.updated events = %d, want 3 (only #2 changed)", n)
	}

	plain := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !plain.OK || plain.ReviewID != first.ReviewID || plain.Version != 2 || plain.PR == nil || plain.PR.Number != 2 {
		t.Fatalf("plain start = %+v, want the unchanged PR review reused at v2", plain)
	}
	if n := countVersions(ctx, t, s, first.ReviewID); n != 2 {
		t.Fatalf("versions = %d, want 2", n)
	}
	if n := countEvents(t, s, first.ReviewID, store.EventPRUpdated); n != 3 {
		t.Fatalf("pr.updated events = %d, want 3 after an unchanged resume", n)
	}
	if n := len(s.openedPRReviews()); n != 3 {
		t.Fatalf("opened PR reviews = %d, want one per start", n)
	}
}

func TestStartPRRefusesToResumeAnotherReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	prStack(t, s, newPRRemote(t))
	writeFile(t, repo, "pending.go", "package p\n")
	if resp := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}); !resp.OK {
		t.Fatalf("local start: %s", resp.Error)
	}

	for _, number := range []int{2, 1} {
		resp := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, PR: &github.PRRef{Repo: prRepo, Number: number}})
		if resp.OK || !strings.Contains(resp.Error, fmt.Sprintf("is not a review of acme/widgets#%d; pass --new", number)) {
			t.Fatalf("pr #%d start over another review = %+v, want a pass --new refusal", number, resp)
		}
		fresh := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, New: true, PR: &github.PRRef{Repo: prRepo, Number: number}})
		if !fresh.OK || fresh.PR == nil || fresh.PR.Number != number {
			t.Fatalf("pr #%d --new start = %+v", number, fresh)
		}
	}
}

func TestStartPROutsideAnyRepoKeysTheReviewToTheCwd(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)
	prStack(t, s, newPRRemote(t))
	cwd := t.TempDir()

	first := prStart(ctx, t, s, cwd, 2)
	sub, err := s.resolver.Store.Get(ctx, first.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Scope != cwd {
		t.Fatalf("scope = %q, want the cwd %q", sub.Scope, cwd)
	}
	plain := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: cwd})
	if !plain.OK || plain.ReviewID != first.ReviewID || plain.PR == nil || plain.Version != 1 {
		t.Fatalf("plain start from the same cwd = %+v, want the PR review reused", plain)
	}
}

func TestRecapturePRTreatsAMovedHeadAsAChange(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	remote := newPRRemote(t)
	gh := prStack(t, s, remote)
	first := prStart(ctx, t, s, repo, 2)

	if err := s.rv.recapturePR(ctx, first.ReviewID); err != nil {
		t.Fatal(err)
	}
	if n := countVersions(ctx, t, s, first.ReviewID); n != 1 {
		t.Fatalf("versions after an unchanged recapture = %d, want 1", n)
	}

	gitRun(t, remote.src, "checkout", "-q", "feat-b")
	gitRun(t, remote.src, "-c", "core.hooksPath=/dev/null", "commit", "-q", "--allow-empty", "-m", "empty")
	moved := strings.TrimSpace(gitRun(t, remote.src, "rev-parse", "HEAD"))
	remote.publish(t, 2, "feat-b")
	gh.UpdatePR(prRepo, 2, func(pr *github.PullRequest) { pr.HeadRefOid = moved })
	gh.SetMergeBase(prRepo, remote.headA, moved, remote.headA)

	if err := s.rv.recapturePR(ctx, first.ReviewID); err != nil {
		t.Fatal(err)
	}
	got := shapes(t, s.latestSections(ctx, t, first.ReviewID))
	if n := countVersions(ctx, t, s, first.ReviewID); n != 2 || got[1].HeadRef != moved || got[1].Paths != "b.go" {
		t.Fatalf("versions = %d, sections = %+v, want v2 with feat-b at the moved head and the same diff", n, got)
	}
	if n := countEvents(t, s, first.ReviewID, store.EventVersionCreated); n != 2 {
		t.Fatalf("version.created events = %d, want 2", n)
	}
	if err := s.rv.recapturePR(ctx, first.ReviewID); err != nil {
		t.Fatal(err)
	}
	if n := countVersions(ctx, t, s, first.ReviewID); n != 2 {
		t.Fatalf("versions after a settled recapture = %d, want 2", n)
	}
}

func TestRecapturePRRefusesALocalReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\n")
	resp := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !resp.OK {
		t.Fatalf("local start: %s", resp.Error)
	}
	if err := s.rv.recapturePR(ctx, resp.ReviewID); err == nil || !strings.Contains(err.Error(), "is not a pull-request review") {
		t.Fatalf("recapturePR(local) = %v, want a refusal", err)
	}
}

func TestRecapturePRKeepsASubmittedReviewSubmitted(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	remote := newPRRemote(t)
	gh := prStack(t, s, remote)
	first := prStart(ctx, t, s, repo, 2)
	if err := s.resolver.Store.SetStatus(ctx, first.ReviewID, "submitted"); err != nil {
		t.Fatal(err)
	}
	statusEvents := countEvents(t, s, first.ReviewID, store.EventStatusChanged)

	gitRun(t, remote.src, "checkout", "-q", "feat-b")
	pushed := remote.commit(t, "c.go", "package c\n")
	remote.publish(t, 2, "feat-b")
	gh.UpdatePR(prRepo, 2, func(pr *github.PullRequest) { pr.HeadRefOid = pushed })
	gh.SetMergeBase(prRepo, remote.headA, pushed, remote.headA)

	if err := s.rv.recapturePR(ctx, first.ReviewID); err != nil {
		t.Fatal(err)
	}
	if n := countVersions(ctx, t, s, first.ReviewID); n != 2 {
		t.Fatalf("versions = %d, want 2", n)
	}
	if status, err := s.reviewStatus(ctx, first.ReviewID); err != nil || status != "submitted" {
		t.Fatalf("status after a poller recapture = %q (%v), want submitted", status, err)
	}
	if n := countEvents(t, s, first.ReviewID, store.EventStatusChanged); n != statusEvents {
		t.Fatalf("status.changed events = %d, want %d", n, statusEvents)
	}

	if resumed := prStart(ctx, t, s, repo, 2); resumed.Version != 2 {
		t.Fatalf("explicit start version = %d, want the recaptured v2", resumed.Version)
	}
	if status, err := s.reviewStatus(ctx, first.ReviewID); err != nil || status != statusOpen {
		t.Fatalf("status after an explicit start = %q (%v), want open", status, err)
	}
}
