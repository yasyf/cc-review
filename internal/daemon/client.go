package daemon

import (
	"context"
	"encoding/json"
	"errors"

	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/cc-interact/procs"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
)

// ReviewClient is the typed control client over cc-interact's generic envelope
// client: it stamps the window pid, marshals each op's domain body, and decodes
// the domain result out of the reply.
type ReviewClient struct {
	c *ccd.Client
}

// NewReviewClient dials the review daemon's control socket.
func NewReviewClient(ctx context.Context) (*ReviewClient, error) {
	c, err := ccd.NewClient(ctx, ccd.ClientConfig{
		Socket: paths.App().SocketPath(), WireBuild: ccd.WireBuild,
	})
	if err != nil {
		return nil, err
	}
	return &ReviewClient{c: c}, nil
}

// Close settles and closes the persistent control session.
func (rc *ReviewClient) Close() error { return rc.c.Close() }

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

// FileStatesByRisk flips every file the organization tags with one of risks to
// the given reviewed/hidden state in one server-resolved batch, returning the
// affected paths. A non-zero aiRequestID ties the batch to that request.
func (rc *ReviewClient) FileStatesByRisk(ctx context.Context, session, cwd string, risks []string, reviewed, hidden *bool, reason string, aiRequestID int64) ([]string, error) {
	_, res, err := rc.do(ctx, OpFileStatesByRisk, session, cwd, body{
		Risk: risks, Reviewed: reviewed, Hidden: hidden, Reason: reason, AIRequestID: aiRequestID,
	})
	if err != nil {
		return nil, err
	}
	return res.Paths, nil
}

// UpdateAIRequestInput carries an AI-request lifecycle move: working/done/failed
// with summary+unmatched, or awaiting_input with a clarifying Question (+optional
// structured Ask) the reviewer answers over REST.
type UpdateAIRequestInput struct {
	Status    string
	Summary   string
	Phase     string
	Unmatched []store.Unmatched
	Question  string
	Ask       *store.Ask
}

// UpdateAIRequest moves an AI request through its lifecycle.
func (rc *ReviewClient) UpdateAIRequest(ctx context.Context, session, cwd string, aiRequestID int64, in UpdateAIRequestInput) error {
	_, _, err := rc.do(ctx, OpUpdateAIRequest, session, cwd, body{
		AIRequestID: aiRequestID, AIStatus: in.Status, Summary: in.Summary, Phase: in.Phase, Unmatched: in.Unmatched,
		Question: in.Question, Ask: in.Ask,
	})
	return err
}

// SubmitOrganization stores the chapter organization for the review's current
// version. partial accepts an in-progress organization (files not yet placed are
// allowed) so the agent can stream chapters as they firm up; the final submit
// must be non-partial to enforce full coverage.
func (rc *ReviewClient) SubmitOrganization(ctx context.Context, session, cwd string, org store.Organization, versionNumber int, partial bool) error {
	_, _, err := rc.do(ctx, OpSubmitOrganization, session, cwd, body{Organization: &org, VersionNumber: versionNumber, Partial: partial})
	return err
}

// ReviewFiles returns the current version's paths (review_files_path,
// organization_path, patch_path) plus a slim inline files subset, optionally
// narrowed by f.
func (rc *ReviewClient) ReviewFiles(ctx context.Context, session, cwd string, f ReviewFilesFilter) (json.RawMessage, error) {
	_, res, err := rc.do(ctx, OpReviewFiles, session, cwd, body{Status: f.Status, Reviewed: f.Reviewed, Hidden: f.Hidden})
	if err != nil {
		return nil, err
	}
	return res.ReviewFiles, nil
}

// Annotate writes Claude-authored line annotations onto the diff: highlights
// (informational line-range marks) and comments (Claude-authored threads). A
// non-zero aiRequestID ties them to that request so undo can remove highlights.
func (rc *ReviewClient) Annotate(ctx context.Context, session, cwd string, items []AnnotateInput, aiRequestID int64) error {
	_, _, err := rc.do(ctx, OpAnnotate, session, cwd, body{Annotations: items, AIRequestID: aiRequestID})
	return err
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

// CloseReview terminally closes a review — this window's (empty ref) or any review
// by slug/id — or, with stale, expires every open review idle past the TTL.
// It returns the rows it closed or expired.
func (rc *ReviewClient) CloseReview(ctx context.Context, session, cwd, ref string, stale bool) ([]ReviewInfo, error) {
	_, res, err := rc.do(ctx, OpClose, session, cwd, body{Ref: ref, Stale: stale})
	if err != nil {
		return nil, err
	}
	return res.Closed, nil
}

// List reports every open review across scopes with its last real activity.
func (rc *ReviewClient) List(ctx context.Context, session, cwd string) ([]ReviewInfo, error) {
	_, res, err := rc.do(ctx, OpList, session, cwd, body{})
	if err != nil {
		return nil, err
	}
	return res.Reviews, nil
}
