package github_test

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/github/githubtest"
)

var repo = github.Repo{Owner: "acme", Name: "widgets"}

const (
	userToken = "user-token"
	appToken  = "app-token"
	botLogin  = "cc-review-alice[bot]"
)

func newServer(t *testing.T) *githubtest.Server {
	t.Helper()
	s := githubtest.New(t)
	s.Login(userToken, "alice")
	s.Login(appToken, botLogin)
	s.SetDefaultBranch(repo, "main")
	return s
}

func TestParsePRRef(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		def     *github.Repo
		want    github.PRRef
		wantErr bool
	}{
		{"url", "https://github.com/acme/widgets/pull/12", nil, github.PRRef{Repo: repo, Number: 12}, false},
		{"url with tab", "https://github.com/acme/widgets/pull/12/files", nil, github.PRRef{Repo: repo, Number: 12}, false},
		{"qualified", "acme/widgets#7", nil, github.PRRef{Repo: repo, Number: 7}, false},
		{"hash", "#7", &repo, github.PRRef{Repo: repo, Number: 7}, false},
		{"bare", "7", &repo, github.PRRef{Repo: repo, Number: 7}, false},
		{"bare without repo", "7", nil, github.PRRef{}, true},
		{"other host", "https://gitlab.com/acme/widgets/pull/12", nil, github.PRRef{}, true},
		{"issue url", "https://github.com/acme/widgets/issues/12", nil, github.PRRef{}, true},
		{"zero", "#0", &repo, github.PRRef{}, true},
		{"not a number", "acme/widgets#x", nil, github.PRRef{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := github.ParsePRRef(tc.in, tc.def)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParsePRRef(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ParsePRRef(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
	if got := (github.PRRef{Repo: repo, Number: 3}).String(); got != "acme/widgets#3" {
		t.Fatalf("String = %q", got)
	}
}

func TestRepoFromRemote(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		want    github.Repo
		wantErr bool
	}{
		{"scp", "git@github.com:acme/widgets.git", repo, false},
		{"https", "https://github.com/acme/widgets", repo, false},
		{"https with .git", "https://github.com/acme/widgets.git", repo, false},
		{"ssh url", "ssh://git@github.com/acme/widgets.git", repo, false},
		{"other host", "git@gitlab.com:acme/widgets.git", github.Repo{}, true},
		{"local path", "/srv/git/widgets.git", github.Repo{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", tc.url}} {
				if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil { //nolint:gosec // G204: test helper running git in a test-controlled temp dir.
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			got, err := github.RepoFromRemote(context.Background(), dir)
			if (err != nil) != tc.wantErr {
				t.Fatalf("RepoFromRemote error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("RepoFromRemote = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPullRequestReadsMetadata(t *testing.T) {
	s := newServer(t)
	seeded := s.AddPR(repo, github.PullRequest{
		Number: 12, Title: "Add widgets", Body: "body", AuthorLogin: "bob",
		HeadRefName: "bob/widgets", HeadRefOid: "aaa", BaseRefName: "main", Draft: true, Mergeable: "MERGEABLE",
		Checks: []github.Check{
			{Name: "build", State: "SUCCESS", URL: "https://ci/1"},
			{Name: "lint", State: "FAILURE", URL: "https://ci/2"},
			{Name: "test", State: "PENDING", URL: "https://ci/3"},
		},
		Reviewers: []github.Reviewer{
			{Login: "carol", AvatarURL: "https://avatars/carol", State: "APPROVED"},
			{Login: "renovate[bot]", AvatarURL: "https://avatars/renovate", State: "COMMENTED"},
			{Login: "dave", AvatarURL: "https://avatars/dave", State: "PENDING"},
		},
	})
	got, err := s.Client(userToken).PullRequest(context.Background(), github.PRRef{Repo: repo, Number: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, seeded) {
		t.Fatalf("PullRequest =\n%+v\nwant\n%+v", got, seeded)
	}
}

func TestPullRequestErrors(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.Client(userToken).PullRequest(ctx, github.PRRef{Repo: repo, Number: 404}); !errors.Is(err, github.ErrNotFound) {
		t.Fatalf("missing PR error = %v, want ErrNotFound", err)
	}
	if _, err := s.Client("stranger").Viewer(ctx); !errors.Is(err, github.ErrUnauthorized) {
		t.Fatalf("unknown token error = %v, want ErrUnauthorized", err)
	}
}

func TestOpenPRsFilterAndPaginate(t *testing.T) {
	s := newServer(t)
	s.PageSize = 1
	s.AddPR(repo, github.PullRequest{Number: 1, HeadRefName: "a", BaseRefName: "main"})
	s.AddPR(repo, github.PullRequest{Number: 2, HeadRefName: "b", BaseRefName: "a"})
	s.AddPR(repo, github.PullRequest{Number: 3, HeadRefName: "c", BaseRefName: "a"})
	s.AddPR(repo, github.PullRequest{Number: 4, HeadRefName: "a", BaseRefName: "main"})
	s.MarkCrossRepository(repo, 4)
	s.AddPR(repo, github.PullRequest{Number: 5, HeadRefName: "d", BaseRefName: "a", State: "MERGED"})
	c := s.Client(userToken)
	ctx := context.Background()

	numbers := func(prs []github.PullRequest) []int {
		out := make([]int, 0, len(prs))
		for _, pr := range prs {
			out = append(out, pr.Number)
		}
		return out
	}
	heads, err := c.OpenPRsWithHead(ctx, repo, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(heads); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("OpenPRsWithHead(a) = %v, want [1]", got)
	}
	bases, err := c.OpenPRsWithBase(ctx, repo, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := numbers(bases); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatalf("OpenPRsWithBase(a) = %v, want [2 3]", got)
	}
}

func TestRepoReads(t *testing.T) {
	s := newServer(t)
	s.SetMergeBase(repo, "main", "feature/x", "base-sha")
	c := s.Client(userToken)
	ctx := context.Background()
	if got, err := c.DefaultBranch(ctx, repo); err != nil || got != "main" {
		t.Fatalf("DefaultBranch = %q, %v", got, err)
	}
	if got, err := c.MergeBase(ctx, repo, "main", "feature/x"); err != nil || got != "base-sha" {
		t.Fatalf("MergeBase = %q, %v", got, err)
	}
	if got, err := c.Viewer(ctx); err != nil || got != "alice" {
		t.Fatalf("Viewer = %q, %v", got, err)
	}
	if got, err := s.Client(appToken).Viewer(ctx); err != nil || got != botLogin {
		t.Fatalf("app Viewer = %q, %v", got, err)
	}
}

func TestSnapshotFollowsEveryPage(t *testing.T) {
	s := newServer(t)
	s.PageSize = 2
	s.AddPR(repo, github.PullRequest{Number: 1, HeadRefName: "a", HeadRefOid: "a1", BaseRefName: "main", Checks: []github.Check{
		{Name: "c1", State: "SUCCESS"}, {Name: "c2", State: "SKIPPED"}, {Name: "c3", State: "NEUTRAL"},
	}})
	s.AddPR(repo, github.PullRequest{Number: 2, HeadRefName: "b", HeadRefOid: "b1", BaseRefName: "a"})
	for i := range 3 {
		comments := []github.RemoteComment{{AuthorLogin: "carol", Body: "first"}}
		if i == 1 {
			comments = append(comments,
				github.RemoteComment{AuthorLogin: botLogin, Body: "second"},
				github.RemoteComment{AuthorLogin: "alice", Body: "third"})
		}
		s.AddThread(repo, 1, github.Thread{Path: "a.go", SubjectType: "LINE", Line: 10 + i, StartLine: 8, OriginalLine: 10 + i, DiffSide: "RIGHT", StartDiffSide: "RIGHT", Comments: comments})
	}
	s.AddThread(repo, 1, github.Thread{Path: "b.go", SubjectType: "FILE", IsOutdated: true, IsResolved: true, Comments: []github.RemoteComment{{AuthorLogin: "carol", Body: "file"}}})
	s.AddThread(repo, 1, github.Thread{Path: "c.go", SubjectType: "LINE", IsOutdated: true, OriginalLine: 4, DiffSide: "LEFT", Comments: []github.RemoteComment{{AuthorLogin: "carol", Body: "old"}}})
	for _, body := range []string{"one", "two", "three"} {
		s.AddIssueComment(repo, 1, github.RemoteComment{AuthorLogin: "dave", Body: body})
	}

	got, err := s.Client(userToken).Snapshot(context.Background(), repo, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2} {
		want := s.Snapshot(repo, n)
		if want.Threads == nil {
			want.Threads = []github.Thread{}
		}
		if want.IssueComments == nil {
			want.IssueComments = []github.RemoteComment{}
		}
		if want.PR.Checks == nil {
			want.PR.Checks = []github.Check{}
		}
		if want.PR.Reviewers == nil {
			want.PR.Reviewers = []github.Reviewer{}
		}
		if !reflect.DeepEqual(got[n], want) {
			t.Errorf("Snapshot #%d =\n%+v\nwant\n%+v", n, got[n], want)
		}
	}
	if len(got[1].Threads) != 5 || len(got[1].Threads[1].Comments) != 3 || len(got[1].IssueComments) != 3 || len(got[1].PR.Checks) != 3 {
		t.Fatalf("Snapshot #1 lost pages: %d threads, %d comments in thread 2, %d issue comments, %d checks",
			len(got[1].Threads), len(got[1].Threads[1].Comments), len(got[1].IssueComments), len(got[1].PR.Checks))
	}
	if login := got[1].Threads[1].Comments[1].AuthorLogin; login != botLogin {
		t.Fatalf("bot comment author = %q, want %q", login, botLogin)
	}
}

func TestWritesRoundTripThroughSnapshot(t *testing.T) {
	s := newServer(t)
	s.AddPR(repo, github.PullRequest{Number: 1, AuthorLogin: "alice", HeadRefName: "a", HeadRefOid: "head1", BaseRefName: "main"})
	ref := github.PRRef{Repo: repo, Number: 1}
	user, app := s.Client(userToken), s.Client(appToken)
	ctx := context.Background()

	line, threadID, err := user.CreateReviewComment(ctx, ref, github.NewReviewComment{
		CommitID: "head1", Path: "a.go", Line: 12, StartLine: 10, Side: "RIGHT", StartSide: "RIGHT", Body: "range",
	})
	if err != nil {
		t.Fatal(err)
	}
	file, fileThreadID, err := user.CreateReviewComment(ctx, ref, github.NewReviewComment{CommitID: "head1", Path: "b.go", SubjectType: "FILE", Body: "whole file"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := app.ReplyToThread(ctx, threadID, "on it")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := user.CreateIssueComment(ctx, ref, "overall")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.ResolveThread(ctx, threadID, true); err != nil {
		t.Fatal(err)
	}
	if err := user.SubmitReview(ctx, ref, "head1", "COMMENT", "looks fine"); err != nil {
		t.Fatal(err)
	}

	snap, err := user.Snapshot(ctx, repo, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	threads := snap[1].Threads
	if len(threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(threads))
	}
	first, second := threads[0], threads[1]
	if first.NodeID != threadID || !first.IsResolved || first.Line != 12 || first.StartLine != 10 || first.DiffSide != "RIGHT" || first.SubjectType != "LINE" {
		t.Errorf("line thread = %+v", first)
	}
	if !reflect.DeepEqual(first.Comments, []github.RemoteComment{line, reply}) {
		t.Errorf("line thread comments = %+v, want %+v", first.Comments, []github.RemoteComment{line, reply})
	}
	if second.NodeID != fileThreadID || second.SubjectType != "FILE" || second.Line != 0 || !reflect.DeepEqual(second.Comments, []github.RemoteComment{file}) {
		t.Errorf("file thread = %+v", second)
	}
	if line.AuthorLogin != "alice" || reply.AuthorLogin != botLogin {
		t.Errorf("authors = %q, %q", line.AuthorLogin, reply.AuthorLogin)
	}
	if !reflect.DeepEqual(snap[1].IssueComments, []github.RemoteComment{issue}) {
		t.Errorf("issue comments = %+v, want %+v", snap[1].IssueComments, issue)
	}

	if err := user.ResolveThread(ctx, threadID, false); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot(repo, 1).Threads[0].IsResolved {
		t.Error("thread still resolved after reopening")
	}

	writes := s.Writes()
	paths := make([]string, 0, len(writes))
	for _, w := range writes {
		paths = append(paths, w.Login+" "+w.Path)
		if wantToken := map[string]string{"alice": userToken, botLogin: appToken}[w.Login]; w.Token != wantToken {
			t.Errorf("write %s token = %q, want %q", w.Path, w.Token, wantToken)
		}
	}
	want := []string{
		"alice /repos/acme/widgets/pulls/1/comments",
		"alice /repos/acme/widgets/pulls/1/comments",
		botLogin + " graphql:AddThreadReply",
		"alice /repos/acme/widgets/issues/1/comments",
		"alice graphql:ResolveThread",
		"alice /repos/acme/widgets/pulls/1/reviews",
		"alice graphql:UnresolveThread",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("writes =\n%q\nwant\n%q", paths, want)
	}
}

func TestWriteFailures(t *testing.T) {
	s := newServer(t)
	s.AddPR(repo, github.PullRequest{Number: 1, AuthorLogin: "alice", HeadRefName: "a", HeadRefOid: "head2", BaseRefName: "main"})
	ref := github.PRRef{Repo: repo, Number: 1}
	c := s.Client(userToken)
	ctx := context.Background()

	var status *github.StatusError
	if _, _, err := c.CreateReviewComment(ctx, ref, github.NewReviewComment{CommitID: "head1", Path: "a.go", Line: 1, Side: "RIGHT", Body: "stale"}); !errors.As(err, &status) || status.Status != 422 {
		t.Fatalf("stale commit error = %v, want 422", err)
	}
	if err := c.SubmitReview(ctx, ref, "head2", "APPROVE", ""); !errors.As(err, &status) || status.Status != 422 {
		t.Fatalf("self-approve error = %v, want 422", err)
	}
	s.FailNext("/repos/acme/widgets/pulls/1/comments", 422, "line must be part of the diff")
	if _, _, err := c.CreateReviewComment(ctx, ref, github.NewReviewComment{CommitID: "head2", Path: "a.go", Line: 1, Side: "RIGHT", Body: "scripted"}); !errors.As(err, &status) || status.Status != 422 || status.Message != "line must be part of the diff" {
		t.Fatalf("scripted failure = %v, want 422", err)
	}
	if _, _, err := c.CreateReviewComment(ctx, ref, github.NewReviewComment{CommitID: "head2", Path: "a.go", Line: 1, Side: "RIGHT", Body: "retry"}); err != nil {
		t.Fatalf("retry after one scripted failure: %v", err)
	}
	if got := len(s.Writes()); got != 1 {
		t.Fatalf("writes after retry = %d, want 1", got)
	}
	s.FailNext("graphql:AddThreadReply", 502, "bad gateway")
	if _, err := c.ReplyToThread(ctx, s.Snapshot(repo, 1).Threads[0].NodeID, "x"); !errors.As(err, &status) || status.Status != 502 {
		t.Fatalf("scripted reply failure = %v, want 502", err)
	}
	s.FailWrites(502, "upstream down")
	if _, err := c.CreateIssueComment(ctx, ref, "x"); !errors.As(err, &status) || status.Status != 502 || status.Message != "upstream down" {
		t.Fatalf("failed write error = %v, want 502 upstream down", err)
	}
	if len(s.Writes()) != 1 {
		t.Fatalf("writes = %+v, want only the retry", s.Writes())
	}
}

func TestSubmitReviewPinsTheCommit(t *testing.T) {
	s := newServer(t)
	s.AddPR(repo, github.PullRequest{Number: 1, AuthorLogin: "bob", HeadRefName: "a", HeadRefOid: "head2", BaseRefName: "main"})
	ref := github.PRRef{Repo: repo, Number: 1}
	if err := s.Client(userToken).SubmitReview(context.Background(), ref, "head1", "APPROVE", "lgtm"); err != nil {
		t.Fatal(err)
	}
	writes := s.Writes()
	if len(writes) != 1 || writes[0].Body["commit_id"] != "head1" {
		t.Fatalf("writes = %+v, want one review pinned to head1", writes)
	}
}

func TestCreateReviewCommentKeepsTheCommentWhenThreadLookupFails(t *testing.T) {
	s := newServer(t)
	s.AddPR(repo, github.PullRequest{Number: 1, AuthorLogin: "alice", HeadRefName: "a", HeadRefOid: "head1", BaseRefName: "main"})
	ref := github.PRRef{Repo: repo, Number: 1}
	c := s.Client(userToken)
	ctx := context.Background()

	s.FailNext("graphql:ThreadOfComment", 502, "bad gateway")
	comment, threadID, err := c.CreateReviewComment(ctx, ref, github.NewReviewComment{CommitID: "head1", Path: "a.go", Line: 1, Side: "RIGHT", Body: "posted"})
	var lookup *github.ThreadLookupError
	var status *github.StatusError
	if !errors.As(err, &lookup) || !errors.As(err, &status) || status.Status != 502 {
		t.Fatalf("CreateReviewComment error = %v, want a ThreadLookupError wrapping 502", err)
	}
	thread := s.Snapshot(repo, 1).Threads[0]
	if threadID != "" || comment.NodeID == "" || comment.NodeID != thread.Comments[0].NodeID || lookup.CommentNodeID != comment.NodeID || comment.URL == "" {
		t.Fatalf("comment = %+v thread %q lookup %+v, want the created comment %s", comment, threadID, lookup, thread.Comments[0].NodeID)
	}
	got, err := c.ThreadForComment(ctx, ref, comment.NodeID)
	if err != nil || got != thread.NodeID {
		t.Fatalf("ThreadForComment = %q %v, want %s", got, err, thread.NodeID)
	}
	if _, err := c.ThreadForComment(ctx, ref, "PRRC_missing"); err == nil {
		t.Fatal("ThreadForComment found a thread for an unknown comment")
	}
	if len(s.Writes()) != 1 {
		t.Fatalf("writes = %+v, want the one comment", s.Writes())
	}
}
