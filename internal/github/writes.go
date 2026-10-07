package github

import "context"

// NewReviewComment starts a review thread. SubjectType FILE comments on the
// whole file and leaves the line fields zero; StartLine zero means one line.
type NewReviewComment struct {
	CommitID    string
	Path        string
	Line        int
	StartLine   int
	Side        string
	StartSide   string
	SubjectType string
	Body        string
}

// CreateReviewComment starts a review thread on ref and returns its first
// comment and the thread's node ID.
func (c *Client) CreateReviewComment(ctx context.Context, ref PRRef, comment NewReviewComment) (RemoteComment, string, error) {
	panic("TODO")
}

// ReplyToReviewComment replies in the thread of review comment inReplyTo.
func (c *Client) ReplyToReviewComment(ctx context.Context, ref PRRef, inReplyTo int64, body string) (RemoteComment, error) {
	panic("TODO")
}

// CreateIssueComment comments on ref's conversation.
func (c *Client) CreateIssueComment(ctx context.Context, ref PRRef, body string) (RemoteComment, error) {
	panic("TODO")
}

// ResolveThread resolves or reopens a review thread.
func (c *Client) ResolveThread(ctx context.Context, threadNodeID string, resolved bool) error {
	panic("TODO")
}

// SubmitReview posts a review on ref. event is COMMENT, APPROVE, or
// REQUEST_CHANGES.
func (c *Client) SubmitReview(ctx context.Context, ref PRRef, event, body string) error {
	panic("TODO")
}
