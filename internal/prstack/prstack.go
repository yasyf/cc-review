// Package prstack resolves the linear stack of open pull requests around one.
package prstack

import (
	"context"
	"fmt"
	"slices"

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

// Resolve walks down from ref to the repository's default branch and up while
// exactly one open pull request is stacked on the current head.
func Resolve(ctx context.Context, c *github.Client, ref github.PRRef) (Stack, error) {
	target, err := c.PullRequest(ctx, ref)
	if err != nil {
		return Stack{}, err
	}
	trunk, err := c.DefaultBranch(ctx, ref.Repo)
	if err != nil {
		return Stack{}, fmt.Errorf("resolve the stack of %s: %w", ref, err)
	}
	prs := []github.PullRequest{target}
	seen := map[int]bool{target.Number: true}
	for bottom := target; bottom.BaseRefName != trunk; {
		parents, err := c.OpenPRsWithHead(ctx, ref.Repo, bottom.BaseRefName)
		if err != nil {
			return Stack{}, fmt.Errorf("resolve the stack of %s: %w", ref, err)
		}
		if len(parents) != 1 {
			return Stack{}, fmt.Errorf("resolve the stack of %s: #%d is based on %s, which is the head of %d open pull requests, not one", ref, bottom.Number, bottom.BaseRefName, len(parents))
		}
		bottom = parents[0]
		if seen[bottom.Number] {
			return Stack{}, fmt.Errorf("resolve the stack of %s: #%d's bases form a cycle", ref, bottom.Number)
		}
		seen[bottom.Number] = true
		prs = slices.Insert(prs, 0, bottom)
	}
	stack := Stack{Repo: ref.Repo, Trunk: trunk, Target: ref.Number}
	for top := target; ; {
		children, err := c.OpenPRsWithBase(ctx, ref.Repo, top.HeadRefName)
		if err != nil {
			return Stack{}, fmt.Errorf("resolve the stack of %s: %w", ref, err)
		}
		if len(children) > 1 {
			for _, child := range children {
				stack.Forks = append(stack.Forks, child.Number)
			}
			slices.Sort(stack.Forks)
			break
		}
		if len(children) == 0 || seen[children[0].Number] {
			break
		}
		top = children[0]
		seen[top.Number] = true
		prs = append(prs, top)
	}
	parentTip := trunk
	for _, pr := range prs {
		mergeBase, err := c.MergeBase(ctx, ref.Repo, parentTip, pr.HeadRefOid)
		if err != nil {
			return Stack{}, fmt.Errorf("resolve the merge base of #%d: %w", pr.Number, err)
		}
		stack.Sections = append(stack.Sections, Section{PR: pr, ParentBranch: pr.BaseRefName, MergeBase: mergeBase})
		parentTip = pr.HeadRefOid
	}
	return stack, nil
}
