package daemon

import (
	"context"
	"encoding/json"
	"errors"

	ccd "github.com/yasyf/cc-interact/daemon"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/procs"
	"github.com/yasyf/cc-review/internal/store"
)

// ReviewClient is the typed control client over cc-interact's generic envelope
// client: it stamps the window pid, marshals each op's domain body, and decodes
// the domain result out of the reply.
type ReviewClient struct {
	c *ccd.Client
}

// NewReviewClient dials the review daemon's control socket.
func NewReviewClient() *ReviewClient {
	return &ReviewClient{c: ccd.NewClient(paths.App().SocketPath())}
}

func (rc *ReviewClient) do(ctx context.Context, op ccd.Op, session, cwd string, b body) (ccd.Reply, result, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return ccd.Reply{}, result{}, err
	}
	reply, err := rc.c.Do(ctx, ccd.Envelope{
		Op: op, Session: session, ClaudePID: procs.ClaudePID(), Scope: cwd, Body: raw,
	})
	if err != nil {
		return ccd.Reply{}, result{}, err
	}
	if !reply.OK {
		return reply, result{}, errors.New(reply.Error)
	}
	var res result
	if len(reply.Body) > 0 {
		if err := json.Unmarshal(reply.Body, &res); err != nil {
			return reply, result{}, err
		}
	}
	return reply, res, nil
}

// Started is the start op's outcome the CLI renders.
type Started struct {
	URL          string
	ReviewID     string
	Version      int
	Resumed      bool
	ChannelState string
	AIRequests   []json.RawMessage
	HTTPPort     int
}

// Start snapshots the working tree and resolves/creates the review.
func (rc *ReviewClient) Start(ctx context.Context, session, cwd string, fresh bool, base string) (Started, error) {
	reply, res, err := rc.do(ctx, OpStart, session, cwd, body{New: fresh, Base: base})
	if err != nil {
		return Started{}, err
	}
	return Started{
		URL: res.URL, ReviewID: reply.SubjectID, Version: res.Version, Resumed: res.Resumed,
		ChannelState: res.ChannelState, AIRequests: res.AIRequests, HTTPPort: reply.HTTPPort,
	}, nil
}

// Reply posts Claude's replies under their comments.
func (rc *ReviewClient) Reply(ctx context.Context, session, cwd string, replies []ReplyInput) error {
	_, _, err := rc.do(ctx, OpReply, session, cwd, body{Replies: replies})
	return err
}

// Feedback reads the frozen feedback JSON for the review.
func (rc *ReviewClient) Feedback(ctx context.Context, session, cwd string) (json.RawMessage, error) {
	_, res, err := rc.do(ctx, OpFeedback, session, cwd, body{})
	if err != nil {
		return nil, err
	}
	return res.Feedback, nil
}

// FileStates batch-sets per-file review state. A non-zero aiRequestID ties the
// changes to that request as one undoable unit.
func (rc *ReviewClient) FileStates(ctx context.Context, session, cwd string, files []FileStateInput, aiRequestID int64) error {
	_, _, err := rc.do(ctx, OpFileStates, session, cwd, body{Files: files, AIRequestID: aiRequestID})
	return err
}

// UpdateAIRequest moves an AI request to working, done, or failed.
func (rc *ReviewClient) UpdateAIRequest(ctx context.Context, session, cwd string, aiRequestID int64, status, summary string, unmatched []store.Unmatched) error {
	_, _, err := rc.do(ctx, OpUpdateAIRequest, session, cwd, body{
		AIRequestID: aiRequestID, AIStatus: status, Summary: summary, Unmatched: unmatched,
	})
	return err
}

// SubmitOrganization stores the chapter organization for the review's current
// version.
func (rc *ReviewClient) SubmitOrganization(ctx context.Context, session, cwd string, org store.Organization, versionNumber int) error {
	_, _, err := rc.do(ctx, OpSubmitOrganization, session, cwd, body{Organization: &org, VersionNumber: versionNumber})
	return err
}

// ReviewFiles returns the current version's files with their review states.
func (rc *ReviewClient) ReviewFiles(ctx context.Context, session, cwd string) (json.RawMessage, error) {
	_, res, err := rc.do(ctx, OpReviewFiles, session, cwd, body{})
	if err != nil {
		return nil, err
	}
	return res.ReviewFiles, nil
}

// TurnStart opens a turn with the pre-edit working-tree snapshot.
func (rc *ReviewClient) TurnStart(ctx context.Context, session, cwd, prompt string) error {
	_, _, err := rc.do(ctx, OpTurnStart, session, cwd, body{Prompt: prompt})
	return err
}

// TurnEnd closes the open turn with the post-edit working-tree snapshot.
func (rc *ReviewClient) TurnEnd(ctx context.Context, session, cwd string) error {
	_, _, err := rc.do(ctx, OpTurnEnd, session, cwd, body{})
	return err
}
