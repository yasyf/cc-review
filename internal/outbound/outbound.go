// Package outbound posts a PR review's local writes to GitHub in one FIFO queue
// per review, recording each write's sync state before the next one starts.
package outbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	ccevent "github.com/yasyf/cc-interact/event"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

// Verdicts a submit posts as each PR's review event.
const (
	VerdictComment        = "COMMENT"
	VerdictApprove        = "APPROVE"
	VerdictRequestChanges = "REQUEST_CHANGES"
)

// ErrNotFailed reports a retry of a write that is not in the failed state.
var ErrNotFailed = errors.New("only a failed write can be retried")

// ErrStopped reports a write queued after the daemon began shutting down. Its
// row stays posting for the next daemon's Start to reconcile.
var ErrStopped = errors.New("the daemon is shutting down")

// AppendFunc persists an event then publishes its subject's wakeup.
type AppendFunc = func(ctx context.Context, e *ccevent.Event) (int64, error)

// AppClient returns a client that writes to repo as the cc-review GitHub App
// and the app's bot login, or an error naming how to set the app up or install
// it on repo.
type AppClient = func(ctx context.Context, repo github.Repo) (*github.Client, string, error)

// Review is one PR's verdict in a stack-wide submit, pinned to the head
// CommitID the reviewer saw.
type Review struct {
	PRNumber int
	CommitID string
	Event    string
	Body     string
}

type queue struct {
	jobs    []func(context.Context)
	running bool
}

// Exclusive holds a review's inbound-apply lock until release. Each write
// stores its remote id under it, so the poller never imports the same comment.
type Exclusive = func(reviewID string) (release func())

// Syncer runs each review's GitHub writes in order: the user's through the user
// token, Claude's through the cc-review GitHub App.
type Syncer struct {
	db        func() *sql.DB
	append    AppendFunc
	user      *github.Client
	app       AppClient
	exclusive Exclusive

	mu      sync.Mutex
	ctx     context.Context
	stopped bool
	running sync.WaitGroup
	queues  map[string]*queue
}

// New builds a Syncer over the daemon's lazy DB accessor and Append chokepoint.
func New(db func() *sql.DB, appendEvent AppendFunc, user *github.Client, app AppClient, exclusive Exclusive) *Syncer {
	return &Syncer{db: db, append: appendEvent, user: user, app: app, exclusive: exclusive, queues: make(map[string]*queue)}
}

// Start runs queued writes under ctx, the daemon's lifetime, and first queues
// every write a previous daemon left posting: an edit is pushed again, and a
// post adopts the comment GitHub already has before posting anew.
func (s *Syncer) Start(ctx context.Context) error {
	writes, err := store.New(s.db()).PostingWrites(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	for _, w := range writes {
		if w.ReplyID != 0 {
			_ = s.later(w.ReviewID, func(ctx context.Context) error { return s.postReply(ctx, w.ReplyID, true) })
			continue
		}
		_ = s.later(w.ReviewID, func(ctx context.Context) error { return s.resumeComment(ctx, w.CommentID) })
	}
	return nil
}

// Stop refuses further writes and waits for the running ones, which end once
// Start's context is cancelled.
func (s *Syncer) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.running.Wait()
}

// Serialize runs fn in reviewID's queue after every write queued before it and
// returns fn's error.
func (s *Syncer) Serialize(ctx context.Context, reviewID string, fn func(context.Context) error) error {
	done := make(chan error, 1)
	if err := s.enqueue(reviewID, func(context.Context) { done <- fn(ctx) }); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CheckApp resolves the app client for repo, failing when the app is not set up
// or not installed there. Claude's writes call it before touching the store.
func (s *Syncer) CheckApp(ctx context.Context, repo github.Repo) error {
	_, _, err := s.app(ctx, repo)
	return err
}

// PostComment queues the post of a comment already stored as posting.
func (s *Syncer) PostComment(reviewID string, commentID int64) {
	_ = s.later(reviewID, func(ctx context.Context) error { return s.postComment(ctx, commentID, false) })
}

// PostCommentNow posts a comment already stored as posting and waits for it.
func (s *Syncer) PostCommentNow(ctx context.Context, reviewID string, commentID int64) error {
	return s.Serialize(ctx, reviewID, func(ctx context.Context) error { return s.postComment(ctx, commentID, false) })
}

// PostReply queues the post of a reply already stored as posting.
func (s *Syncer) PostReply(reviewID string, replyID int64) {
	_ = s.later(reviewID, func(ctx context.Context) error { return s.postReply(ctx, replyID, false) })
}

// PostReplyNow posts a reply already stored as posting and waits for it.
func (s *Syncer) PostReplyNow(ctx context.Context, reviewID string, replyID int64) error {
	return s.Serialize(ctx, reviewID, func(ctx context.Context) error { return s.postReply(ctx, replyID, false) })
}

type edit struct {
	body, status *string
	seq          int64
}

// SyncEdit marks a comment posting and queues mirroring its current body
// and/or open/resolved status onto GitHub. A PR conversation comment has no
// thread, so its status stays local.
func (s *Syncer) SyncEdit(ctx context.Context, reviewID string, commentID int64, body, status bool) error {
	c, err := store.New(s.db()).GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	e := edit{seq: c.EditSeq}
	if body {
		e.body = &c.Body
	}
	if status && !conversation(c) {
		e.status = &c.Status
	}
	if e.body == nil && e.status == nil {
		return nil
	}
	if err := s.markComment(ctx, commentID); err != nil {
		return err
	}
	return s.later(reviewID, func(ctx context.Context) error { return s.syncEdit(ctx, commentID, e) })
}

// Retry re-drives a failed write: the reply when replyID is set, else the
// comment's post, or its body and resolve state once it exists on GitHub.
func (s *Syncer) Retry(ctx context.Context, reviewID string, commentID, replyID int64) error {
	st := store.New(s.db())
	if replyID != 0 {
		r, err := st.GetReply(ctx, replyID)
		if err != nil {
			return err
		}
		if r.CommentID != commentID {
			return fmt.Errorf("reply %d is not on comment %d: %w", replyID, commentID, store.ErrNotFound)
		}
		if r.SyncState != store.SyncFailed {
			return fmt.Errorf("reply %d is %s: %w", replyID, r.SyncState, ErrNotFailed)
		}
		if err := s.markReply(ctx, r); err != nil {
			return err
		}
		return s.later(reviewID, func(ctx context.Context) error { return s.postReply(ctx, replyID, true) })
	}
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if c.SyncState != store.SyncFailed {
		return fmt.Errorf("comment %d is %s: %w", commentID, c.SyncState, ErrNotFailed)
	}
	if c.RemoteID != "" {
		return s.SyncEdit(ctx, reviewID, commentID, true, true)
	}
	if err := s.markComment(ctx, commentID); err != nil {
		return err
	}
	return s.later(reviewID, func(ctx context.Context) error { return s.postComment(ctx, commentID, true) })
}

// SubmitReviews posts one review per PR with the user token, after every write
// already queued for the review, stopping at the first failure.
func (s *Syncer) SubmitReviews(ctx context.Context, reviewID string, repo github.Repo, reviews []Review) error {
	return s.Serialize(ctx, reviewID, func(ctx context.Context) error {
		for _, r := range reviews {
			if err := s.user.SubmitReview(ctx, github.PRRef{Repo: repo, Number: r.PRNumber}, r.CommitID, r.Event, r.Body); err != nil {
				return fmt.Errorf("submit review on #%d: %w", r.PRNumber, err)
			}
		}
		return nil
	})
}

func (s *Syncer) later(reviewID string, job func(context.Context) error) error {
	return s.enqueue(reviewID, func(ctx context.Context) { _ = job(ctx) })
}

func (s *Syncer) enqueue(reviewID string, job func(context.Context)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrStopped
	}
	q, ok := s.queues[reviewID]
	if !ok {
		q = &queue{}
		s.queues[reviewID] = q
	}
	q.jobs = append(q.jobs, job)
	if !q.running {
		q.running = true
		s.running.Add(1)
		go s.drain(s.ctx, reviewID, q)
	}
	return nil
}

func (s *Syncer) drain(ctx context.Context, reviewID string, q *queue) {
	defer s.running.Done()
	for {
		s.mu.Lock()
		if len(q.jobs) == 0 {
			q.running = false
			delete(s.queues, reviewID)
			s.mu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		s.mu.Unlock()
		release := s.exclusive(reviewID)
		job(ctx)
		release()
	}
}

func (s *Syncer) resumeComment(ctx context.Context, commentID int64) error {
	c, err := store.New(s.db()).GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if c.RemoteID != "" {
		return s.syncEdit(ctx, commentID, resumedEdit(c))
	}
	return s.postComment(ctx, commentID, true)
}

func resumedEdit(c store.Comment) edit {
	e := edit{body: &c.Body, seq: c.EditSeq}
	if !conversation(c) {
		e.status = &c.Status
	}
	return e
}

func (s *Syncer) postComment(ctx context.Context, commentID int64, adopt bool) error {
	st := store.New(s.db())
	c, err := st.GetComment(ctx, commentID)
	if err != nil || c.RemoteID != "" {
		return err
	}
	var remote github.RemoteComment
	var threadID string
	found := false
	if adopt {
		if remote, threadID, found, err = s.findComment(ctx, st, c); err != nil {
			err = fmt.Errorf("check GitHub for an earlier post: %w", err)
		}
	}
	if err == nil && !found {
		remote, threadID, err = s.createComment(ctx, st, c)
	}
	if err != nil {
		return s.finishComment(ctx, c.ID, c.EditSeq, store.SyncFailed, remote.NodeID, "", remote.URL, err)
	}
	if c.Status == "resolved" && threadID != "" {
		err = s.user.ResolveThread(ctx, threadID, true)
	}
	state := store.SyncSynced
	if err != nil {
		state = store.SyncFailed
	}
	return s.finishComment(ctx, c.ID, c.EditSeq, state, remote.NodeID, threadID, remote.URL, err)
}

func (s *Syncer) createComment(ctx context.Context, st *store.Store, c store.Comment) (github.RemoteComment, string, error) {
	pr, sec, client, err := s.target(ctx, st, c, c.Author)
	if err != nil {
		return github.RemoteComment{}, "", err
	}
	if conversation(c) {
		remote, err := client.CreateIssueComment(ctx, pr, c.Body)
		return remote, "", err
	}
	return client.CreateReviewComment(ctx, pr, newReviewComment(c, sec.HeadRef))
}

func (s *Syncer) postReply(ctx context.Context, replyID int64, adopt bool) error {
	st := store.New(s.db())
	r, err := st.GetReply(ctx, replyID)
	if err != nil || r.RemoteID != "" {
		return err
	}
	var remote github.RemoteComment
	found := false
	if adopt {
		if remote, found, err = s.findReply(ctx, st, r); err != nil {
			err = fmt.Errorf("check GitHub for an earlier post: %w", err)
		}
	}
	if err == nil && !found {
		remote, err = s.createReply(ctx, st, r)
	}
	if err != nil {
		return s.finishReply(ctx, r, store.SyncFailed, "", "", err)
	}
	return s.finishReply(ctx, r, store.SyncSynced, remote.NodeID, remote.URL, nil)
}

func (s *Syncer) createReply(ctx context.Context, st *store.Store, r store.Reply) (github.RemoteComment, error) {
	parent, err := st.GetComment(ctx, r.CommentID)
	if err != nil {
		return github.RemoteComment{}, err
	}
	if parent.RemoteID == "" {
		return github.RemoteComment{}, fmt.Errorf("comment %d is not on GitHub yet: retry it first", parent.ID)
	}
	pr, _, client, err := s.target(ctx, st, parent, r.Origin)
	if err != nil {
		return github.RemoteComment{}, err
	}
	if conversation(parent) {
		return client.CreateIssueComment(ctx, pr, r.GitHubBody())
	}
	return client.ReplyToThread(ctx, parent.RemoteThreadID, r.GitHubBody())
}

func (s *Syncer) syncEdit(ctx context.Context, commentID int64, e edit) error {
	st := store.New(s.db())
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	c.RemoteThreadID, err = s.threadOf(ctx, st, c)
	if err == nil {
		err = s.pushEdit(ctx, st, c, e)
	}
	state := store.SyncSynced
	if err != nil {
		state = store.SyncFailed
	}
	return s.finishComment(ctx, c.ID, e.seq, state, "", c.RemoteThreadID, "", err)
}

func (s *Syncer) threadOf(ctx context.Context, st *store.Store, c store.Comment) (string, error) {
	if c.RemoteID == "" || c.RemoteThreadID != "" || conversation(c) {
		return c.RemoteThreadID, nil
	}
	pr, _, _, err := s.target(ctx, st, c, store.AuthorUser)
	if err != nil {
		return "", err
	}
	return s.user.ThreadForComment(ctx, pr, c.RemoteID)
}

func (s *Syncer) pushEdit(ctx context.Context, st *store.Store, c store.Comment, e edit) error {
	if c.RemoteID == "" {
		return fmt.Errorf("comment %d is not on GitHub yet: retry it first", c.ID)
	}
	if e.body != nil {
		_, _, client, err := s.target(ctx, st, c, c.Author)
		if err != nil {
			return err
		}
		update := client.UpdateReviewComment
		if conversation(c) {
			update = client.UpdateIssueComment
		}
		if _, err := update(ctx, c.RemoteID, *e.body); err != nil {
			return err
		}
	}
	if e.status != nil && c.RemoteThreadID != "" {
		return s.user.ResolveThread(ctx, c.RemoteThreadID, *e.status == "resolved")
	}
	return nil
}

func (s *Syncer) target(ctx context.Context, st *store.Store, c store.Comment, author string) (github.PRRef, store.Section, *github.Client, error) {
	sec, err := st.GetSection(ctx, c.SectionID)
	if err != nil {
		return github.PRRef{}, store.Section{}, nil, err
	}
	reviewID, _, err := st.ResolveCommentContext(ctx, c.ID)
	if err != nil {
		return github.PRRef{}, store.Section{}, nil, err
	}
	meta, _, err := st.GetReviewMeta(ctx, reviewID)
	if err != nil {
		return github.PRRef{}, store.Section{}, nil, err
	}
	repo, err := ParseRepo(meta.Repo)
	if err != nil {
		return github.PRRef{}, store.Section{}, nil, err
	}
	pr := github.PRRef{Repo: repo, Number: sec.PRNumber}
	if author != store.AuthorClaude {
		return pr, sec, s.user, nil
	}
	client, _, err := s.app(ctx, repo)
	return pr, sec, client, err
}

func (s *Syncer) markComment(ctx context.Context, commentID int64) error {
	st := store.New(s.db())
	if err := st.SetCommentSync(ctx, commentID, store.SyncPosting, "", "", "", ""); err != nil {
		return err
	}
	return s.emitComment(ctx, st, commentID, nil)
}

func (s *Syncer) markReply(ctx context.Context, r store.Reply) error {
	return s.finishReply(ctx, r, store.SyncPosting, "", "", nil)
}

func (s *Syncer) finishComment(ctx context.Context, commentID, seq int64, state, remoteID, threadID, url string, cause error) error {
	st := store.New(s.db())
	if err := st.AckCommentSync(ctx, commentID, seq, state, remoteID, threadID, url, errText(cause)); err != nil {
		return errors.Join(cause, err)
	}
	return s.emitComment(ctx, st, commentID, cause)
}

func (s *Syncer) emitComment(ctx context.Context, st *store.Store, commentID int64, cause error) error {
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.emitSynced(ctx, st, commentID, wire.CommentSyncedFields(c)))
}

func (s *Syncer) finishReply(ctx context.Context, r store.Reply, state, remoteID, url string, cause error) error {
	st := store.New(s.db())
	if err := st.SetReplySync(ctx, r.ID, state, remoteID, url, errText(cause)); err != nil {
		return errors.Join(cause, err)
	}
	r, err := st.GetReply(ctx, r.ID)
	if err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.emitSynced(ctx, st, r.CommentID, wire.ReplySyncedFields(r)))
}

// emitSynced uses the agent origin to keep a browser-only badge update off
// Claude's channel, as organization.updated does.
func (s *Syncer) emitSynced(ctx context.Context, st *store.Store, commentID int64, fields map[string]any) error {
	reviewID, version, err := st.ResolveCommentContext(ctx, commentID)
	if err != nil {
		return err
	}
	_, err = s.append(ctx, &ccevent.Event{
		SubjectID: reviewID, Origin: ccevent.OriginAgent, Type: store.EventCommentSynced,
		Payload: wire.Event(store.EventCommentSynced, version, fields),
	})
	return err
}

// ParseRepo splits a review_meta repo ("owner/name").
func ParseRepo(s string) (github.Repo, error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return github.Repo{}, fmt.Errorf("repo %q: want owner/name", s)
	}
	return github.Repo{Owner: owner, Name: name}, nil
}

func newReviewComment(c store.Comment, headSHA string) github.NewReviewComment {
	nc := github.NewReviewComment{CommitID: headSHA, Path: c.FilePath, Body: c.Body}
	if c.Subject == "file" {
		nc.SubjectType = "FILE"
		return nc
	}
	endSide := c.Side
	if c.EndSide != "" {
		endSide = c.EndSide
	}
	startSide := endSide
	if c.StartSide != "" {
		startSide = c.StartSide
	}
	nc.Line, nc.Side = c.EndLine, diffSide(endSide)
	if c.StartLine != c.EndLine || diffSide(startSide) != nc.Side {
		nc.StartLine, nc.StartSide = c.StartLine, diffSide(startSide)
	}
	return nc
}

func diffSide(side string) string {
	if side == "deletions" {
		return "LEFT"
	}
	return "RIGHT"
}

func conversation(c store.Comment) bool {
	return c.Subject == "file" && c.FilePath == ""
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
