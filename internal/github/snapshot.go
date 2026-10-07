package github

import (
	"context"
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

// Snapshot reads every numbered pull request of repo in one query, following
// pages of threads and comments past the first hundred.
func (c *Client) Snapshot(ctx context.Context, repo Repo, numbers []int) (map[int]PRSnapshot, error) {
	panic("TODO")
}
