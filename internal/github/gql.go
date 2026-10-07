package github

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

const pageSize = 100

const prMetaFragment = `
fragment PRMeta on PullRequest {
  id number title body state isDraft url mergeable updatedAt
  headRefName headRefOid baseRefName isCrossRepository
  author { __typename login avatarUrl }
  statusCheckRollup { id contexts(first: 100) { pageInfo { hasNextPage endCursor } nodes { ...CheckContext } } }
  latestReviews(first: 100) { nodes { state author { __typename login avatarUrl } } }
  reviewRequests(first: 100) { nodes { requestedReviewer {
    __typename
    ... on User { login avatarUrl }
    ... on Bot { login avatarUrl }
    ... on Mannequin { login avatarUrl }
    ... on Team { combinedSlug avatarUrl }
  } } }
}` + checkContextFragment

const checkContextFragment = `
fragment CheckContext on StatusCheckRollupContext {
  __typename
  ... on CheckRun { name status conclusion detailsUrl }
  ... on StatusContext { context state targetUrl }
}`

const reviewCommentFragment = `
fragment ReviewCommentFields on PullRequestReviewComment {
  id fullDatabaseId body url createdAt updatedAt author { __typename login avatarUrl }
}`

const issueCommentFragment = `
fragment IssueCommentFields on IssueComment {
  id fullDatabaseId body url createdAt updatedAt author { __typename login avatarUrl }
}`

const threadFragment = `
fragment ThreadFields on PullRequestReviewThread {
  id isResolved isOutdated path subjectType line startLine originalLine originalStartLine diffSide startDiffSide
  comments(first: 100) { pageInfo { hasNextPage endCursor } nodes { ...ReviewCommentFields } }
}` + reviewCommentFragment

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type connection[T any] struct {
	PageInfo pageInfo `json:"pageInfo"`
	Nodes    []T      `json:"nodes"`
}

type gqlActor struct {
	Typename     string `json:"__typename"`
	Login        string `json:"login"`
	CombinedSlug string `json:"combinedSlug"`
	AvatarURL    string `json:"avatarUrl"`
}

func actorLogin(a *gqlActor) string {
	switch {
	case a == nil:
		return "ghost"
	case a.Typename == "Bot":
		return a.Login + "[bot]"
	case a.Typename == "Team":
		return a.CombinedSlug
	}
	return a.Login
}

func actorAvatar(a *gqlActor) string {
	if a == nil {
		return ""
	}
	return a.AvatarURL
}

type gqlCheckContext struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"detailsUrl"`
	Context    string `json:"context"`
	State      string `json:"state"`
	TargetURL  string `json:"targetUrl"`
}

type gqlPR struct {
	ID                string    `json:"id"`
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	Body              string    `json:"body"`
	State             string    `json:"state"`
	IsDraft           bool      `json:"isDraft"`
	URL               string    `json:"url"`
	Mergeable         string    `json:"mergeable"`
	UpdatedAt         time.Time `json:"updatedAt"`
	HeadRefName       string    `json:"headRefName"`
	HeadRefOid        string    `json:"headRefOid"`
	BaseRefName       string    `json:"baseRefName"`
	IsCrossRepository bool      `json:"isCrossRepository"`
	Author            *gqlActor `json:"author"`
	StatusCheckRollup *struct {
		ID       string                      `json:"id"`
		Contexts connection[gqlCheckContext] `json:"contexts"`
	} `json:"statusCheckRollup"`
	LatestReviews struct {
		Nodes []struct {
			State  string    `json:"state"`
			Author *gqlActor `json:"author"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer *gqlActor `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	ReviewThreads connection[gqlThread]  `json:"reviewThreads"`
	Comments      connection[gqlComment] `json:"comments"`
}

type gqlThread struct {
	ID                string                 `json:"id"`
	IsResolved        bool                   `json:"isResolved"`
	IsOutdated        bool                   `json:"isOutdated"`
	Path              string                 `json:"path"`
	SubjectType       string                 `json:"subjectType"`
	Line              *int                   `json:"line"`
	StartLine         *int                   `json:"startLine"`
	OriginalLine      *int                   `json:"originalLine"`
	OriginalStartLine *int                   `json:"originalStartLine"`
	DiffSide          string                 `json:"diffSide"`
	StartDiffSide     string                 `json:"startDiffSide"`
	Comments          connection[gqlComment] `json:"comments"`
}

type gqlComment struct {
	ID             string    `json:"id"`
	FullDatabaseID string    `json:"fullDatabaseId"`
	Body           string    `json:"body"`
	URL            string    `json:"url"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Author         *gqlActor `json:"author"`
}

func (g gqlComment) remote() (RemoteComment, error) {
	id, err := strconv.ParseInt(g.FullDatabaseID, 10, 64)
	if err != nil {
		return RemoteComment{}, fmt.Errorf("github: comment %s database id %q: %w", g.ID, g.FullDatabaseID, err)
	}
	return RemoteComment{
		NodeID:          g.ID,
		DatabaseID:      id,
		AuthorLogin:     actorLogin(g.Author),
		AuthorAvatarURL: actorAvatar(g.Author),
		Body:            g.Body,
		URL:             g.URL,
		CreatedAt:       g.CreatedAt,
		UpdatedAt:       g.UpdatedAt,
	}, nil
}

func remoteComments(nodes []gqlComment) ([]RemoteComment, error) {
	out := make([]RemoteComment, 0, len(nodes))
	for _, n := range nodes {
		c, err := n.remote()
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func followPages[T any](ctx context.Context, c *Client, operation, id, on, field, selection, fragments string, first connection[T]) ([]T, error) {
	nodes := first.Nodes
	query := fmt.Sprintf(`query %s($id: ID!, $after: String!) { node(id: $id) { ... on %s { %s(first: %d, after: $after) { pageInfo { hasNextPage endCursor } nodes { %s } } } } }%s`,
		operation, on, field, pageSize, selection, fragments)
	for page := first.PageInfo; page.HasNextPage; {
		var out struct {
			Node map[string]connection[T] `json:"node"`
		}
		if err := c.GraphQL(ctx, query, map[string]any{"id": id, "after": page.EndCursor}, &out); err != nil {
			return nil, fmt.Errorf("github: page %s of %s: %w", field, id, err)
		}
		next := out.Node[field]
		nodes = append(nodes, next.Nodes...)
		page = next.PageInfo
	}
	return nodes, nil
}

func (c *Client) pullRequestOf(ctx context.Context, g gqlPR) (PullRequest, error) {
	pr := PullRequest{
		Number:      g.Number,
		NodeID:      g.ID,
		Title:       g.Title,
		Body:        g.Body,
		State:       g.State,
		URL:         g.URL,
		AuthorLogin: actorLogin(g.Author),
		HeadRefName: g.HeadRefName,
		HeadRefOid:  g.HeadRefOid,
		BaseRefName: g.BaseRefName,
		Draft:       g.IsDraft,
		Mergeable:   g.Mergeable,
		Checks:      []Check{},
		Reviewers:   reviewersOf(g),
		UpdatedAt:   g.UpdatedAt,
	}
	if g.StatusCheckRollup == nil {
		return pr, nil
	}
	contexts, err := followPages(ctx, c, "CheckContextsPage", g.StatusCheckRollup.ID, "StatusCheckRollup", "contexts", "...CheckContext",
		checkContextFragment, g.StatusCheckRollup.Contexts)
	if err != nil {
		return PullRequest{}, err
	}
	for _, check := range contexts {
		pr.Checks = append(pr.Checks, checkOf(check))
	}
	return pr, nil
}

func checkOf(g gqlCheckContext) Check {
	if g.Typename == "StatusContext" {
		state := map[string]string{"SUCCESS": "SUCCESS", "PENDING": "PENDING", "EXPECTED": "PENDING", "FAILURE": "FAILURE", "ERROR": "FAILURE"}[g.State]
		return Check{Name: g.Context, State: state, URL: g.TargetURL}
	}
	state := "FAILURE"
	switch {
	case g.Status != "COMPLETED":
		state = "PENDING"
	case g.Conclusion == "SUCCESS", g.Conclusion == "NEUTRAL", g.Conclusion == "SKIPPED":
		state = g.Conclusion
	case g.Conclusion == "STALE":
		state = "NEUTRAL"
	}
	return Check{Name: g.Name, State: state, URL: g.DetailsURL}
}

func reviewersOf(g gqlPR) []Reviewer {
	reviewers := []Reviewer{}
	index := map[string]int{}
	add := func(r Reviewer) {
		if i, ok := index[r.Login]; ok {
			reviewers[i] = r
			return
		}
		index[r.Login] = len(reviewers)
		reviewers = append(reviewers, r)
	}
	for _, review := range g.LatestReviews.Nodes {
		if review.State == "DISMISSED" || review.State == "PENDING" {
			continue
		}
		add(Reviewer{Login: actorLogin(review.Author), AvatarURL: actorAvatar(review.Author), State: review.State})
	}
	for _, request := range g.ReviewRequests.Nodes {
		add(Reviewer{Login: actorLogin(request.RequestedReviewer), AvatarURL: actorAvatar(request.RequestedReviewer), State: "PENDING"})
	}
	return reviewers
}
