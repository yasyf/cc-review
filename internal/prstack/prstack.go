// Package prstack resolves the linear stack of open pull requests around one.
package prstack

import (
	"context"

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
	panic("TODO")
}
