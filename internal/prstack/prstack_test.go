package prstack

import (
	"context"
	"fmt"
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

func TestResolveFloorsAtABaseNoOpenPRHeads(t *testing.T) {
	s, c := newServer(t)
	addPR(s, 2, "b", "graphite-base/1")
	addPR(s, 3, "c", "b")
	s.SetMergeBase(repo, "graphite-base/1", "b-sha", "m1")
	s.SetMergeBase(repo, "b-sha", "c-sha", "m2")

	for _, target := range []int{2, 3} {
		stack, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: target})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"b<graphite-base/1@m1", "c<b@m2"}; !reflect.DeepEqual(sections(stack), want) {
			t.Fatalf("sections from #%d = %v, want %v", target, sections(stack), want)
		}
		if stack.Trunk != "main" {
			t.Fatalf("Trunk from #%d = %q, want main", target, stack.Trunk)
		}
	}
}

func TestResolveRefusesABaseTwoOpenPRsHead(t *testing.T) {
	s, c := newServer(t)
	addPR(s, 1, "a", "main")
	s.AddPR(repo, github.PullRequest{Number: 4, HeadRefName: "a", HeadRefOid: "a-sha", BaseRefName: "main"})
	addPR(s, 2, "b", "a")
	_, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: 2})
	if err == nil || !strings.Contains(err.Error(), "head of 2 open pull requests") {
		t.Fatalf("Resolve = %v, want an ambiguous-parent error", err)
	}
}

func nativeStack(s *githubtest.Server, size int) []int {
	numbers := make([]int, size)
	base, baseTip := "main", "main"
	for i := range size {
		number, head, sha := i+1, fmt.Sprintf("b%d", i), fmt.Sprintf("%040x", i+1)
		s.AddPR(repo, github.PullRequest{Number: number, HeadRefName: head, HeadRefOid: sha, BaseRefName: base})
		s.SetMergeBase(repo, baseTip, sha, fmt.Sprintf("m%d", i))
		numbers[i] = number
		base, baseTip = head, sha
	}
	s.SetNativeStack(repo, numbers...)
	return numbers
}

func TestResolveReadsANativeStackInTheSameReadsAtAnySize(t *testing.T) {
	for _, size := range []int{2, 50} {
		s, c := newServer(t)
		numbers := nativeStack(s, size)
		ref := github.PRRef{Repo: repo, Number: numbers[0]}
		if _, err := Resolve(context.Background(), c, ref); err != nil {
			t.Fatal(err)
		}
		before := s.Reads()
		stack, err := Resolve(context.Background(), c, ref)
		if err != nil {
			t.Fatal(err)
		}
		if len(stack.Sections) != size || stack.Sections[size-1].MergeBase != fmt.Sprintf("m%d", size-1) {
			t.Fatalf("size %d: sections = %v", size, sections(stack))
		}
		if reads := s.Reads() - before; reads != 3 {
			t.Fatalf("size %d: resolving again took %d reads, want the stack read, the top's children and the trunk merge base", size, reads)
		}
	}
}

func TestResolveLeavesLandedEntriesOutOfANativeStack(t *testing.T) {
	s, c := newServer(t)
	s.AddPR(repo, github.PullRequest{Number: 1, HeadRefName: "a", HeadRefOid: "a-sha", BaseRefName: "main", State: "MERGED"})
	addPR(s, 2, "b", "a")
	addPR(s, 3, "c", "b")
	s.SetNativeStack(repo, 1, 2, 3)
	s.SetMergeBase(repo, "a", "b-sha", "m1")
	s.SetMergeBase(repo, "b-sha", "c-sha", "m2")

	stack, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: 3})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b<a@m1", "c<b@m2"}; !reflect.DeepEqual(sections(stack), want) {
		t.Fatalf("sections = %v, want %v", sections(stack), want)
	}
}

func TestResolveWalksPastBothEndsOfANativeStack(t *testing.T) {
	s, c := newServer(t)
	addPR(s, 1, "a", "main")
	addPR(s, 2, "b", "a")
	addPR(s, 3, "c", "b")
	addPR(s, 4, "d", "c")
	addPR(s, 5, "e", "c")
	s.SetNativeStack(repo, 2, 3)
	s.SetMergeBase(repo, "main", "a-sha", "m0")
	s.SetMergeBase(repo, "a-sha", "b-sha", "m1")
	s.SetMergeBase(repo, "b-sha", "c-sha", "m2")

	stack, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: 3})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a<main@m0", "b<a@m1", "c<b@m2"}; !reflect.DeepEqual(sections(stack), want) {
		t.Fatalf("sections = %v, want %v", sections(stack), want)
	}
	if !reflect.DeepEqual(stack.Forks, []int{4, 5}) {
		t.Fatalf("Forks = %v, want [4 5]", stack.Forks)
	}
}

func TestResolveKeepsOnlyTheLinkedRunOfANativeStack(t *testing.T) {
	s, c := newServer(t)
	addPR(s, 1, "a", "main")
	addPR(s, 2, "b", "elsewhere")
	addPR(s, 3, "c", "b")
	s.SetNativeStack(repo, 1, 2, 3)
	s.SetMergeBase(repo, "elsewhere", "b-sha", "m1")
	s.SetMergeBase(repo, "b-sha", "c-sha", "m2")

	stack, err := Resolve(context.Background(), c, github.PRRef{Repo: repo, Number: 3})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b<elsewhere@m1", "c<b@m2"}; !reflect.DeepEqual(sections(stack), want) {
		t.Fatalf("sections = %v, want %v", sections(stack), want)
	}
}
