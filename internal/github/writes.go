package github

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

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

const threadOfCommentQuery = `query ThreadOfComment($owner: String!, $name: String!, $number: Int!, $before: String) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) {
    reviewThreads(last: 100, before: $before) {
      pageInfo { hasPreviousPage startCursor }
      nodes { id comments(first: 1) { nodes { id } } }
    }
  } }
}`

// ThreadLookupError reports a review comment GitHub created whose thread could
// not be found. CreateReviewComment still returns the created comment with it;
// ThreadForComment retries the lookup.
type ThreadLookupError struct {
	CommentNodeID string
	Err           error
}

func (e *ThreadLookupError) Error() string { return e.Err.Error() }

func (e *ThreadLookupError) Unwrap() error { return e.Err }

type restComment struct {
	ID        int64     `json:"id"`
	NodeID    string    `json:"node_id"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
}

func (r restComment) remote() RemoteComment {
	return RemoteComment{
		NodeID:          r.NodeID,
		DatabaseID:      r.ID,
		AuthorLogin:     r.User.Login,
		AuthorAvatarURL: r.User.AvatarURL,
		Body:            r.Body,
		URL:             r.HTMLURL,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func pullPath(ref PRRef, suffix string) string {
	return "repos/" + ref.Repo.String() + "/pulls/" + strconv.Itoa(ref.Number) + suffix
}

// CreateReviewComment starts a review thread on ref and returns its first
// comment and the thread's node ID. When GitHub created the comment but its
// thread lookup fails, it returns the comment with a *ThreadLookupError.
func (c *Client) CreateReviewComment(ctx context.Context, ref PRRef, comment NewReviewComment) (RemoteComment, string, error) {
	body := map[string]any{"body": comment.Body, "commit_id": comment.CommitID, "path": comment.Path}
	if comment.SubjectType == "FILE" {
		body["subject_type"] = "file"
	} else {
		body["line"], body["side"] = comment.Line, comment.Side
		if comment.StartLine != 0 {
			body["start_line"], body["start_side"] = comment.StartLine, comment.StartSide
		}
	}
	var out restComment
	if err := c.REST(ctx, http.MethodPost, pullPath(ref, "/comments"), body, &out); err != nil {
		return RemoteComment{}, "", fmt.Errorf("comment on %s %s: %w", ref, comment.Path, err)
	}
	remote := out.remote()
	thread, err := c.ThreadForComment(ctx, ref, remote.NodeID)
	if err != nil {
		return remote, "", &ThreadLookupError{CommentNodeID: remote.NodeID, Err: err}
	}
	return remote, thread, nil
}

// ThreadForComment returns the node ID of the review thread on ref whose first
// comment is commentNodeID.
func (c *Client) ThreadForComment(ctx context.Context, ref PRRef, commentNodeID string) (string, error) {
	vars := map[string]any{"owner": ref.Repo.Owner, "name": ref.Repo.Name, "number": ref.Number}
	for {
		var out struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						PageInfo struct {
							HasPreviousPage bool   `json:"hasPreviousPage"`
							StartCursor     string `json:"startCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							ID       string                 `json:"id"`
							Comments connection[gqlComment] `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		if err := c.GraphQL(ctx, threadOfCommentQuery, vars, &out); err != nil {
			return "", fmt.Errorf("find the thread of comment %s on %s: %w", commentNodeID, ref, err)
		}
		threads := out.Repository.PullRequest.ReviewThreads
		for _, t := range threads.Nodes {
			if len(t.Comments.Nodes) == 1 && t.Comments.Nodes[0].ID == commentNodeID {
				return t.ID, nil
			}
		}
		if !threads.PageInfo.HasPreviousPage {
			return "", fmt.Errorf("github: no review thread on %s starts with comment %s", ref, commentNodeID)
		}
		vars["before"] = threads.PageInfo.StartCursor
	}
}

// ReplyToReviewComment replies in the thread of review comment inReplyTo.
func (c *Client) ReplyToReviewComment(ctx context.Context, ref PRRef, inReplyTo int64, body string) (RemoteComment, error) {
	var out restComment
	path := pullPath(ref, "/comments/"+strconv.FormatInt(inReplyTo, 10)+"/replies")
	if err := c.REST(ctx, http.MethodPost, path, map[string]string{"body": body}, &out); err != nil {
		return RemoteComment{}, fmt.Errorf("reply to comment %d on %s: %w", inReplyTo, ref, err)
	}
	return out.remote(), nil
}

// CreateIssueComment comments on ref's conversation.
func (c *Client) CreateIssueComment(ctx context.Context, ref PRRef, body string) (RemoteComment, error) {
	var out restComment
	path := "repos/" + ref.Repo.String() + "/issues/" + strconv.Itoa(ref.Number) + "/comments"
	if err := c.REST(ctx, http.MethodPost, path, map[string]string{"body": body}, &out); err != nil {
		return RemoteComment{}, fmt.Errorf("comment on %s: %w", ref, err)
	}
	return out.remote(), nil
}

// ResolveThread resolves or reopens a review thread.
func (c *Client) ResolveThread(ctx context.Context, threadNodeID string, resolved bool) error {
	mutation := `mutation UnresolveThread($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { thread { id } } }`
	if resolved {
		mutation = `mutation ResolveThread($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id } } }`
	}
	var out map[string]any
	if err := c.GraphQL(ctx, mutation, map[string]any{"id": threadNodeID}, &out); err != nil {
		return fmt.Errorf("set thread %s resolved=%t: %w", threadNodeID, resolved, err)
	}
	return nil
}

// SubmitReview posts a review on ref, pinned to commitID. event is COMMENT,
// APPROVE, or REQUEST_CHANGES.
func (c *Client) SubmitReview(ctx context.Context, ref PRRef, commitID, event, body string) error {
	payload := map[string]string{"commit_id": commitID, "event": event, "body": body}
	if err := c.REST(ctx, http.MethodPost, pullPath(ref, "/reviews"), payload, nil); err != nil {
		return fmt.Errorf("submit %s review on %s: %w", event, ref, err)
	}
	return nil
}
