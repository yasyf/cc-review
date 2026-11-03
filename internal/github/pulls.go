package github

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

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

const stackedPullRequestQuery = `query StackedPullRequest($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      ...PRMeta
      stack { entries(first: 100) { nodes { pullRequest { ...PRMeta } } } }
    }
  }
}` + prMetaFragment

const openPRsQuery = `query OpenPRs($owner: String!, $name: String!, $head: String, $base: String, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(headRefName: $head, baseRefName: $base, states: OPEN, first: 100, after: $after, orderBy: {field: CREATED_AT, direction: ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes { ...PRMeta }
    }
  }
}` + prMetaFragment

// StackedPullRequest reads one pull request and, in the same read, the open
// pull requests of its native GitHub stack, trunk-most first. A pull request
// outside a native stack has none.
func (c *Client) StackedPullRequest(ctx context.Context, ref PRRef) (PullRequest, []PullRequest, error) {
	var out struct {
		Repository struct {
			PullRequest struct {
				gqlPR
				Stack *struct {
					Entries struct {
						Nodes []struct {
							PullRequest gqlPR `json:"pullRequest"`
						} `json:"nodes"`
					} `json:"entries"`
				} `json:"stack"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": ref.Repo.Owner, "name": ref.Repo.Name, "number": ref.Number}
	if err := c.read(ctx, ref.Repo, stackedPullRequestQuery, vars, &out); err != nil {
		return PullRequest{}, nil, fmt.Errorf("read %s: %w", ref, err)
	}
	read := out.Repository.PullRequest
	target, err := c.pullRequestOf(ctx, ref.Repo, read.gqlPR)
	if err != nil {
		return PullRequest{}, nil, err
	}
	if read.Stack == nil {
		return target, nil, nil
	}
	var open []PullRequest
	for _, entry := range read.Stack.Entries.Nodes {
		if entry.PullRequest.State != "OPEN" || entry.PullRequest.IsCrossRepository {
			continue
		}
		pr, err := c.pullRequestOf(ctx, ref.Repo, entry.PullRequest)
		if err != nil {
			return PullRequest{}, nil, err
		}
		open = append(open, pr)
	}
	return target, open, nil
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
		if err := c.read(ctx, repo, openPRsQuery, vars, &out); err != nil {
			return nil, fmt.Errorf("list open pull requests of %s %v: %w", repo, filter, err)
		}
		for _, g := range out.Repository.PullRequests.Nodes {
			if g.IsCrossRepository {
				continue
			}
			pr, err := c.pullRequestOf(ctx, repo, g)
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

// MergeBase reads the merge base of base and head, each a sha or branch. The
// merge base of two shas never changes, so it is read once per client.
func (c *Client) MergeBase(ctx context.Context, repo Repo, base, head string) (string, error) {
	key := repo.String() + "/" + base + "..." + head
	if known, ok := c.mergeBases.Load(key); ok {
		return known.(string), nil
	}
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
	if fullSHA.MatchString(base) && fullSHA.MatchString(head) {
		c.mergeBases.Store(key, out.MergeBaseCommit.SHA)
	}
	return out.MergeBaseCommit.SHA, nil
}

// Viewer reads the login the client's token authenticates as, once per client.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	c.viewerMu.Lock()
	defer c.viewerMu.Unlock()
	if c.viewer != "" {
		return c.viewer, nil
	}
	var out struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := c.GraphQL(ctx, `query Viewer { viewer { login } }`, nil, &out); err != nil {
		return "", err
	}
	c.viewer = out.Viewer.Login
	return c.viewer, nil
}

func escapeRef(ref string) string { return (&url.URL{Path: ref}).EscapedPath() }
