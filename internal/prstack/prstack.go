// Package prstack resolves the linear stack of open pull requests around one.
package prstack

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/yasyf/cc-review/internal/github"
)

// Section is one pull request of a stack with the branch it is based on and
// the merge base its diff starts from.
type Section struct {
	PR           github.PullRequest
	ParentBranch string
	MergeBase    string
}

// Stack is the linear stack through Target, trunk-most first. Forks lists the
// open pull requests stacked on the top section when it has more than one.
type Stack struct {
	Repo     github.Repo
	Trunk    string
	Target   int
	Sections []Section
	Forks    []int
}

const mergeBaseReads = 4

// Resolve walks down from ref to the repository's default branch, or to a base
// branch no open pull request heads (a landed parent's branch, or Graphite's
// graphite-base/<n> during a restack), and up while exactly one open pull
// request is stacked on the current head. The two walks, and every section's
// merge base, are read concurrently.
func Resolve(ctx context.Context, c *github.Client, ref github.PRRef) (Stack, error) {
	var target github.PullRequest
	var trunk string
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		target, err = c.PullRequest(gctx, ref)
		return err
	})
	g.Go(func() (err error) {
		if trunk, err = c.DefaultBranch(gctx, ref.Repo); err != nil {
			return fmt.Errorf("resolve the stack of %s: %w", ref, err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return Stack{}, err
	}
	stack := Stack{Repo: ref.Repo, Trunk: trunk, Target: ref.Number}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	up := make(chan upWalk, 1)
	go func() { up <- walkUp(ctx, c, ref, target) }()
	prs, floor, err := walkDown(ctx, c, ref, target, trunk)
	if err != nil {
		return Stack{}, err
	}
	above := <-up
	stack.Forks = above.forks
	for _, pr := range above.prs {
		if slices.ContainsFunc(prs, func(p github.PullRequest) bool { return p.Number == pr.Number }) {
			stack.Forks, above.err = nil, nil
			break
		}
		prs = append(prs, pr)
	}
	if above.err != nil {
		return Stack{}, above.err
	}
	stack.Sections = make([]Section, len(prs))
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(mergeBaseReads)
	for i, pr := range prs {
		parentTip := floor
		if i > 0 {
			parentTip = prs[i-1].HeadRefOid
		}
		g.Go(func() error {
			mergeBase, err := c.MergeBase(gctx, ref.Repo, parentTip, pr.HeadRefOid)
			if err != nil {
				return fmt.Errorf("resolve the merge base of #%d: %w", pr.Number, err)
			}
			stack.Sections[i] = Section{PR: pr, ParentBranch: pr.BaseRefName, MergeBase: mergeBase}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return Stack{}, err
	}
	return stack, nil
}

// walkDown returns target and the pull requests below it, trunk-most first,
// and the branch the bottom one is based on.
func walkDown(ctx context.Context, c *github.Client, ref github.PRRef, target github.PullRequest, trunk string) ([]github.PullRequest, string, error) {
	prs := []github.PullRequest{target}
	seen := map[int]bool{target.Number: true}
	for bottom := target; bottom.BaseRefName != trunk; {
		parents, err := c.OpenPRsWithHead(ctx, ref.Repo, bottom.BaseRefName)
		if err != nil {
			return nil, "", fmt.Errorf("resolve the stack of %s: %w", ref, err)
		}
		if len(parents) == 0 {
			return prs, bottom.BaseRefName, nil
		}
		if len(parents) > 1 {
			return nil, "", fmt.Errorf("resolve the stack of %s: #%d is based on %s, which is the head of %d open pull requests, not one", ref, bottom.Number, bottom.BaseRefName, len(parents))
		}
		bottom = parents[0]
		if seen[bottom.Number] {
			return nil, "", fmt.Errorf("resolve the stack of %s: #%d's bases form a cycle", ref, bottom.Number)
		}
		seen[bottom.Number] = true
		prs = slices.Insert(prs, 0, bottom)
	}
	return prs, trunk, nil
}

type upWalk struct {
	prs   []github.PullRequest
	forks []int
	err   error
}

// walkUp returns the pull requests stacked above target, bottom-most first,
// and the open pull requests on the top one when more than one is. On error it
// keeps the pull requests read so far, so the caller can tell whether the
// error lies past where the walk would have stopped.
func walkUp(ctx context.Context, c *github.Client, ref github.PRRef, target github.PullRequest) upWalk {
	var w upWalk
	seen := map[int]bool{target.Number: true}
	for top := target; ; {
		children, err := c.OpenPRsWithBase(ctx, ref.Repo, top.HeadRefName)
		if err != nil {
			w.err = fmt.Errorf("resolve the stack of %s: %w", ref, err)
			return w
		}
		if len(children) > 1 {
			for _, child := range children {
				w.forks = append(w.forks, child.Number)
			}
			slices.Sort(w.forks)
			return w
		}
		if len(children) == 0 || seen[children[0].Number] {
			return w
		}
		top = children[0]
		seen[top.Number] = true
		w.prs = append(w.prs, top)
	}
}
