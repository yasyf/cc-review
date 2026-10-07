package github

import (
	"context"
	"fmt"
	"maps"
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

const pullRequestQuery = `query PullRequest($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { ...PRMeta } }
}` + prMetaFragment

const openPRsQuery = `query OpenPRs($owner: String!, $name: String!, $head: String, $base: String, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(headRefName: $head, baseRefName: $base, states: OPEN, first: 100, after: $after, orderBy: {field: CREATED_AT, direction: ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes { ...PRMeta }
    }
  }
}` + prMetaFragment

// PullRequest reads one pull request.
func (c *Client) PullRequest(ctx context.Context, ref PRRef) (PullRequest, error) {
	var out struct {
		Repository struct {
			PullRequest gqlPR `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": ref.Repo.Owner, "name": ref.Repo.Name, "number": ref.Number}
	if err := c.GraphQL(ctx, pullRequestQuery, vars, &out); err != nil {
		return PullRequest{}, fmt.Errorf("read %s: %w", ref, err)
	}
	return c.pullRequestOf(ctx, out.Repository.PullRequest)
}

// OpenPRsWithHead lists the open pull requests from repo's own branch.
func (c *Client) OpenPRsWithHead(ctx context.Context, repo Repo, branch string) ([]PullRequest, error) {
	return c.openPRs(ctx, repo, map[string]any{"head": branch})
}

// OpenPRsWithBase lists the open pull requests from repo's own branches whose
// base is branch.
func (c *Client) OpenPRsWithBase(ctx context.Context, repo Repo, branch string) ([]PullRequest, error) {
	return c.openPRs(ctx, repo, map[string]any{"base": branch})
}

func (c *Client) openPRs(ctx context.Context, repo Repo, filter map[string]any) ([]PullRequest, error) {
	prs := []PullRequest{}
	vars := map[string]any{"owner": repo.Owner, "name": repo.Name}
	maps.Copy(vars, filter)
	for {
		var out struct {
			Repository struct {
				PullRequests connection[gqlPR] `json:"pullRequests"`
			} `json:"repository"`
		}
		if err := c.GraphQL(ctx, openPRsQuery, vars, &out); err != nil {
			return nil, fmt.Errorf("list open pull requests of %s %v: %w", repo, filter, err)
		}
		for _, g := range out.Repository.PullRequests.Nodes {
			if g.IsCrossRepository {
				continue
			}
			pr, err := c.pullRequestOf(ctx, g)
			if err != nil {
				return nil, err
			}
			prs = append(prs, pr)
		}
		page := out.Repository.PullRequests.PageInfo
		if !page.HasNextPage {
			return prs, nil
		}
		vars["after"] = page.EndCursor
	}
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
