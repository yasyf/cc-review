package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Thread is one review thread. SubjectType is LINE or FILE; DiffSide and
// StartDiffSide are LEFT or RIGHT. An outdated thread has no Line.
type Thread struct {
	NodeID            string
	IsResolved        bool
	IsOutdated        bool
	Path              string
	SubjectType       string
	Line              int
	StartLine         int
	OriginalLine      int
	OriginalStartLine int
	DiffSide          string
	StartDiffSide     string
	Comments          []RemoteComment
}

// RemoteComment is one review or issue comment. A bot author's login carries
// its [bot] suffix, whichever API returned it.
type RemoteComment struct {
	NodeID          string
	DatabaseID      int64
	AuthorLogin     string
	AuthorAvatarURL string
	Body            string
	URL             string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PRSnapshot is everything inbound sync reads about one pull request.
type PRSnapshot struct {
	PR            PullRequest
	Threads       []Thread
	IssueComments []RemoteComment
}

const prSyncFragment = `
fragment PRSync on PullRequest {
  ...PRMeta
  reviewThreads(first: 100) { pageInfo { hasNextPage endCursor } nodes { ...ThreadFields } }
  comments(first: 100) { pageInfo { hasNextPage endCursor } nodes { ...IssueCommentFields } }
}` + prMetaFragment + threadFragment + issueCommentFragment

// Snapshot reads every numbered pull request of repo in one query, following
// pages of threads and comments past the first hundred.
func (c *Client) Snapshot(ctx context.Context, repo Repo, numbers []int) (map[int]PRSnapshot, error) {
	var query strings.Builder
	query.WriteString("query Snapshot($owner: String!, $name: String!) {\n  repository(owner: $owner, name: $name) {\n")
	for _, n := range numbers {
		fmt.Fprintf(&query, "    pr%d: pullRequest(number: %d) { ...PRSync }\n", n, n)
	}
	query.WriteString("  }\n}" + prSyncFragment)
	var out struct {
		Repository map[string]gqlPR `json:"repository"`
	}
	if err := c.GraphQL(ctx, query.String(), map[string]any{"owner": repo.Owner, "name": repo.Name}, &out); err != nil {
		return nil, fmt.Errorf("snapshot %s %v: %w", repo, numbers, err)
	}
	snaps := make(map[int]PRSnapshot, len(numbers))
	for _, n := range numbers {
		g, ok := out.Repository["pr"+strconv.Itoa(n)]
		if !ok {
			return nil, fmt.Errorf("snapshot %s: no pull request #%d in the response", repo, n)
		}
		snap, err := c.snapshotOf(ctx, g)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s#%d: %w", repo, n, err)
		}
		snaps[n] = snap
	}
	return snaps, nil
}

func (c *Client) snapshotOf(ctx context.Context, g gqlPR) (PRSnapshot, error) {
	pr, err := c.pullRequestOf(ctx, g)
	if err != nil {
		return PRSnapshot{}, err
	}
	threads, err := followPages(ctx, c, "ReviewThreadsPage", g.ID, "PullRequest", "reviewThreads", "...ThreadFields", threadFragment, g.ReviewThreads)
	if err != nil {
		return PRSnapshot{}, err
	}
	snap := PRSnapshot{PR: pr, Threads: make([]Thread, 0, len(threads))}
	for _, t := range threads {
		thread, err := c.threadOf(ctx, t)
		if err != nil {
			return PRSnapshot{}, err
		}
		snap.Threads = append(snap.Threads, thread)
	}
	issueComments, err := followPages(ctx, c, "IssueCommentsPage", g.ID, "PullRequest", "comments", "...IssueCommentFields", issueCommentFragment, g.Comments)
	if err != nil {
		return PRSnapshot{}, err
	}
	if snap.IssueComments, err = remoteComments(issueComments); err != nil {
		return PRSnapshot{}, err
	}
	return snap, nil
}

func (c *Client) threadOf(ctx context.Context, t gqlThread) (Thread, error) {
	nodes, err := followPages(ctx, c, "ThreadCommentsPage", t.ID, "PullRequestReviewThread", "comments", "...ReviewCommentFields", reviewCommentFragment, t.Comments)
	if err != nil {
		return Thread{}, err
	}
	comments, err := remoteComments(nodes)
	if err != nil {
		return Thread{}, err
	}
	return Thread{
		NodeID:            t.ID,
		IsResolved:        t.IsResolved,
		IsOutdated:        t.IsOutdated,
		Path:              t.Path,
		SubjectType:       t.SubjectType,
		Line:              orZero(t.Line),
		StartLine:         orZero(t.StartLine),
		OriginalLine:      orZero(t.OriginalLine),
		OriginalStartLine: orZero(t.OriginalStartLine),
		DiffSide:          t.DiffSide,
		StartDiffSide:     t.StartDiffSide,
		Comments:          comments,
	}, nil
}

func orZero(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}
