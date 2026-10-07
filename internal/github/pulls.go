package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// PullRequest is one pull request's metadata, checks, and reviewers.
type PullRequest struct {
	Number      int
	NodeID      string
	Title       string
	Body        string
	State       string
	URL         string
	AuthorLogin string
	HeadRefName string
	HeadRefOid  string
	BaseRefName string
	Draft       bool
	Mergeable   string
	Checks      []Check
	Reviewers   []Reviewer
	UpdatedAt   time.Time
}

// Check is one status check on a pull request's head. State is SUCCESS,
// FAILURE, PENDING, NEUTRAL, or SKIPPED.
type Check struct {
	Name  string
	State string
	URL   string
}

// Reviewer is a requested or reviewing user or team. State is APPROVED,
// CHANGES_REQUESTED, COMMENTED, or PENDING.
type Reviewer struct {
	Login     string
	AvatarURL string
	State     string
}

// PullRequest reads one pull request.
func (c *Client) PullRequest(ctx context.Context, ref PRRef) (PullRequest, error) {
	panic("TODO")
}

// OpenPRsWithHead lists the open pull requests whose head is branch in repo.
func (c *Client) OpenPRsWithHead(ctx context.Context, repo Repo, branch string) ([]PullRequest, error) {
	panic("TODO")
}

// OpenPRsWithBase lists the open pull requests from repo's own branches whose
// base is branch.
func (c *Client) OpenPRsWithBase(ctx context.Context, repo Repo, branch string) ([]PullRequest, error) {
	panic("TODO")
}

// DefaultBranch reads repo's default branch.
func (c *Client) DefaultBranch(ctx context.Context, repo Repo) (string, error) {
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.REST(ctx, http.MethodGet, "repos/"+repo.String(), nil, &out); err != nil {
		return "", err
	}
	return out.DefaultBranch, nil
}

// MergeBase reads the merge base of base and head, each a sha or branch.
func (c *Client) MergeBase(ctx context.Context, repo Repo, base, head string) (string, error) {
	var out struct {
		MergeBaseCommit struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}
	path := "repos/" + repo.String() + "/compare/" + escapeRef(base) + "..." + escapeRef(head) + "?per_page=1"
	if err := c.REST(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	if out.MergeBaseCommit.SHA == "" {
		return "", fmt.Errorf("github: compare %s...%s in %s named no merge base", base, head, repo)
	}
	return out.MergeBaseCommit.SHA, nil
}

// Viewer reads the login the client's token authenticates as.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	var out struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := c.GraphQL(ctx, `query Viewer { viewer { login } }`, nil, &out); err != nil {
		return "", err
	}
	return out.Viewer.Login, nil
}

func escapeRef(ref string) string { return (&url.URL{Path: ref}).EscapedPath() }
