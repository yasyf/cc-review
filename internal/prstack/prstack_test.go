package prstack

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/github/githubtest"
)

var repo = github.Repo{Owner: "acme", Name: "widgets"}

func newServer(t *testing.T) (*githubtest.Server, *github.Client) {
	t.Helper()
	s := githubtest.New(t)
	s.Login("token", "alice")
	s.SetDefaultBranch(repo, "main")
	return s, s.Client("token")
}

func addPR(s *githubtest.Server, number int, head, base string) {
	s.AddPR(repo, github.PullRequest{Number: number, HeadRefName: head, HeadRefOid: head + "-sha", BaseRefName: base})
}

func sections(stack Stack) []string {
	out := make([]string, 0, len(stack.Sections))
	for _, sec := range stack.Sections {
		out = append(out, sec.PR.HeadRefName+"<"+sec.ParentBranch+"@"+sec.MergeBase)
	}
	return out
}

func TestResolveLinearStack(t *testing.T) {
	s, c := newServer(t)
	addPR(s, 1, "a", "main")
	addPR(s, 2, "b", "a")
	addPR(s, 3, "c", "b")
	addPR(s, 9, "other", "main")
	s.SetMergeBase(repo, "main", "a-sha", "m0")
	s.SetMergeBase(repo, "a-sha", "b-sha", "m1")
	s.SetMergeBase(repo, "b-sha", "c-sha", "m2")

	for _, target := range []int{1, 2, 3} {
		stack, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: target})
		if err != nil {
			t.Fatal(err)
		}
		if stack.Trunk != "main" || stack.Target != target || stack.Repo != repo || stack.Forks != nil {
			t.Fatalf("stack from #%d = %+v", target, stack)
		}
		want := []string{"a<main@m0", "b<a@m1", "c<b@m2"}
		if got := sections(stack); !reflect.DeepEqual(got, want) {
			t.Fatalf("sections from #%d = %v, want %v", target, got, want)
		}
	}
}

func TestResolveStopsAtAFork(t *testing.T) {
	s, c := newServer(t)
	addPR(s, 1, "a", "main")
	addPR(s, 2, "b", "a")
	addPR(s, 4, "d", "b")
	addPR(s, 3, "c", "b")
	addPR(s, 5, "e", "c")
	s.SetMergeBase(repo, "main", "a-sha", "m0")
	s.SetMergeBase(repo, "a-sha", "b-sha", "m1")

	stack, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a<main@m0", "b<a@m1"}; !reflect.DeepEqual(sections(stack), want) {
		t.Fatalf("sections = %v, want %v", sections(stack), want)
	}
	if !reflect.DeepEqual(stack.Forks, []int{3, 4}) {
		t.Fatalf("Forks = %v, want [3 4]", stack.Forks)
	}
}

func TestResolveRefusesABaseNoOpenPRHeads(t *testing.T) {
	s, c := newServer(t)
	addPR(s, 2, "b", "landed")
	_, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: 2})
	if err == nil || !strings.Contains(err.Error(), "head of 0 open pull requests") {
		t.Fatalf("Resolve = %v, want a missing-parent error", err)
	}
}
