// Package outbound posts a PR review's local writes to GitHub in one FIFO queue
// per review, recording each write's sync state before the next one starts.
package outbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	ccevent "github.com/yasyf/cc-interact/event"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

const (
	SyncLocal   = "local"
	SyncPosting = "posting"
	SyncSynced  = "synced"
	SyncFailed  = "failed"

	VerdictComment        = "COMMENT"
	VerdictApprove        = "APPROVE"
	VerdictRequestChanges = "REQUEST_CHANGES"
)

// ErrNotFailed reports a retry of a write that is not in the failed state.
var ErrNotFailed = errors.New("only a failed write can be retried")

// AppendFunc persists an event then publishes its subject's wakeup.
type AppendFunc = func(ctx context.Context, e *ccevent.Event) (int64, error)

// AppClient returns a client that writes to repo as the cc-review GitHub App,
// or an error naming how to set the app up or install it on repo.
type AppClient = func(ctx context.Context, repo github.Repo) (*github.Client, error)

// Review is one PR's verdict in a stack-wide submit, pinned to the head
// CommitID the reviewer saw.
type Review struct {
	PRNumber int
	CommitID string
	Event    string
	Body     string
}

type queue struct {
	jobs    []func()
	running bool
}

// Syncer runs each review's GitHub writes in order: the user's through the user
// token, Claude's through the cc-review GitHub App.
type Syncer struct {
	db     func() *sql.DB
	append AppendFunc
	user   *github.Client
	app    AppClient

	mu     sync.Mutex
	queues map[string]*queue
}

// New builds a Syncer over the daemon's lazy DB accessor and Append chokepoint.
func New(db func() *sql.DB, append AppendFunc, user *github.Client, app AppClient) *Syncer {
	return &Syncer{db: db, append: append, user: user, app: app, queues: make(map[string]*queue)}
}

// Serialize runs fn in reviewID's queue after every write queued before it and
// returns fn's error. The inbound poller applies each snapshot through it, so a
// comment GitHub created before the poll fetched always has its remote id stored
// before the poll matches against it.
func (s *Syncer) Serialize(ctx context.Context, reviewID string, fn func(context.Context) error) error {
	done := make(chan error, 1)
	s.enqueue(reviewID, func() { done <- fn(ctx) })
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
	_, err := s.app(ctx, repo)
	return err
}

// PostComment queues the post of a comment already stored as posting.
func (s *Syncer) PostComment(ctx context.Context, reviewID string, commentID int64) {
	s.later(ctx, reviewID, func(ctx context.Context) error { return s.postComment(ctx, commentID) })
}

// PostCommentNow posts a comment already stored as posting and waits for it.
func (s *Syncer) PostCommentNow(ctx context.Context, reviewID string, commentID int64) error {
	return s.Serialize(ctx, reviewID, func(ctx context.Context) error { return s.postComment(ctx, commentID) })
}

// PostReply queues the post of a reply already stored as posting.
func (s *Syncer) PostReply(ctx context.Context, reviewID string, replyID int64) {
	s.later(ctx, reviewID, func(ctx context.Context) error { return s.postReply(ctx, replyID) })
}

// PostReplyNow posts a reply already stored as posting and waits for it.
func (s *Syncer) PostReplyNow(ctx context.Context, reviewID string, replyID int64) error {
	return s.Serialize(ctx, reviewID, func(ctx context.Context) error { return s.postReply(ctx, replyID) })
}

// SyncResolved marks a comment posting and queues mirroring its current
// open/resolved status onto its GitHub thread.
func (s *Syncer) SyncResolved(ctx context.Context, reviewID string, commentID int64) error {
	if err := s.markComment(ctx, commentID); err != nil {
		return err
	}
	s.later(ctx, reviewID, func(ctx context.Context) error { return s.syncResolved(ctx, commentID) })
	return nil
}

// Retry re-drives a failed write: the reply when replyID is set, else the
// comment's post, or its resolve state once the comment already exists on
// GitHub.
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
		if r.SyncState != SyncFailed {
			return fmt.Errorf("reply %d is %s: %w", replyID, r.SyncState, ErrNotFailed)
		}
		if err := s.markReply(ctx, r); err != nil {
			return err
		}
		s.PostReply(ctx, reviewID, replyID)
		return nil
	}
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if c.SyncState != SyncFailed {
		return fmt.Errorf("comment %d is %s: %w", commentID, c.SyncState, ErrNotFailed)
	}
	if c.RemoteID != "" {
		return s.SyncResolved(ctx, reviewID, commentID)
	}
	if err := s.markComment(ctx, commentID); err != nil {
		return err
	}
	s.PostComment(ctx, reviewID, commentID)
	return nil
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

func (s *Syncer) later(ctx context.Context, reviewID string, job func(context.Context) error) {
	ctx = context.WithoutCancel(ctx)
	s.enqueue(reviewID, func() { _ = job(ctx) })
}

func (s *Syncer) enqueue(reviewID string, job func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.queues[reviewID]
	if !ok {
		q = &queue{}
		s.queues[reviewID] = q
	}
	q.jobs = append(q.jobs, job)
	if !q.running {
		q.running = true
		go s.drain(reviewID, q)
	}
}

func (s *Syncer) drain(reviewID string, q *queue) {
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
		job()
	}
}

func (s *Syncer) postComment(ctx context.Context, commentID int64) error {
	st := store.New(s.db())
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	remote, threadID, err := s.createComment(ctx, st, c)
	if err != nil {
		return s.finishComment(ctx, c.ID, SyncFailed, "", "", "", err)
	}
	if c.Status == "resolved" {
		err = s.user.ResolveThread(ctx, threadID, true)
	}
	state := SyncSynced
	if err != nil {
		state = SyncFailed
	}
	return s.finishComment(ctx, c.ID, state, remoteID(remote), threadID, remote.URL, err)
}

func (s *Syncer) createComment(ctx context.Context, st *store.Store, c store.Comment) (github.RemoteComment, string, error) {
	pr, sec, client, err := s.target(ctx, st, c, c.Author)
	if err != nil {
		return github.RemoteComment{}, "", err
	}
	return client.CreateReviewComment(ctx, pr, newReviewComment(c, sec.HeadRef))
}

func (s *Syncer) postReply(ctx context.Context, replyID int64) error {
	st := store.New(s.db())
	r, err := st.GetReply(ctx, replyID)
	if err != nil {
		return err
	}
	remote, err := s.createReply(ctx, st, r)
	if err != nil {
		return s.finishReply(ctx, r, SyncFailed, "", "", err)
	}
	return s.finishReply(ctx, r, SyncSynced, remoteID(remote), remote.URL, nil)
}

func (s *Syncer) createReply(ctx context.Context, st *store.Store, r store.Reply) (github.RemoteComment, error) {
	parent, err := st.GetComment(ctx, r.CommentID)
	if err != nil {
		return github.RemoteComment{}, err
	}
	if parent.RemoteID == "" {
		return github.RemoteComment{}, fmt.Errorf("comment %d is not on GitHub yet: retry it first", parent.ID)
	}
	inReplyTo, err := strconv.ParseInt(parent.RemoteID, 10, 64)
	if err != nil {
		return github.RemoteComment{}, fmt.Errorf("comment %d remote id %q: %w", parent.ID, parent.RemoteID, err)
	}
	pr, _, client, err := s.target(ctx, st, parent, r.Origin)
	if err != nil {
		return github.RemoteComment{}, err
	}
	return client.ReplyToReviewComment(ctx, pr, inReplyTo, replyBody(r))
}

func (s *Syncer) syncResolved(ctx context.Context, commentID int64) error {
	st := store.New(s.db())
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if c.RemoteThreadID == "" {
		return s.finishComment(ctx, c.ID, SyncFailed, c.RemoteID, "", c.RemoteURL,
			fmt.Errorf("comment %d has no GitHub thread to resolve", c.ID))
	}
	if err := s.user.ResolveThread(ctx, c.RemoteThreadID, c.Status == "resolved"); err != nil {
		return s.finishComment(ctx, c.ID, SyncFailed, c.RemoteID, c.RemoteThreadID, c.RemoteURL, err)
	}
	return s.finishComment(ctx, c.ID, SyncSynced, c.RemoteID, c.RemoteThreadID, c.RemoteURL, nil)
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
	if author != store.OriginClaude {
		return pr, sec, s.user, nil
	}
	client, err := s.app(ctx, repo)
	return pr, sec, client, err
}

func (s *Syncer) markComment(ctx context.Context, commentID int64) error {
	st := store.New(s.db())
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	return s.finishComment(ctx, c.ID, SyncPosting, c.RemoteID, c.RemoteThreadID, c.RemoteURL, nil)
}

func (s *Syncer) markReply(ctx context.Context, r store.Reply) error {
	return s.finishReply(ctx, r, SyncPosting, r.RemoteID, r.RemoteURL, nil)
}

func (s *Syncer) finishComment(ctx context.Context, commentID int64, state, remoteID, threadID, url string, cause error) error {
	st := store.New(s.db())
	if err := st.SetCommentSync(ctx, commentID, state, remoteID, threadID, url, errText(cause)); err != nil {
		return errors.Join(cause, err)
	}
	reviewID, version, err := st.ResolveCommentContext(ctx, commentID)
	if err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.emitSynced(ctx, reviewID, version, map[string]any{
		"commentId": strconv.FormatInt(commentID, 10), "syncState": state, "syncError": errText(cause), "remoteUrl": url,
	}))
}

func (s *Syncer) finishReply(ctx context.Context, r store.Reply, state, remoteID, url string, cause error) error {
	st := store.New(s.db())
	if err := st.SetReplySync(ctx, r.ID, state, remoteID, url, errText(cause)); err != nil {
		return errors.Join(cause, err)
	}
	reviewID, version, err := st.ResolveCommentContext(ctx, r.CommentID)
	if err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.emitSynced(ctx, reviewID, version, map[string]any{
		"commentId": strconv.FormatInt(r.CommentID, 10), "replyId": strconv.FormatInt(r.ID, 10),
		"syncState": state, "syncError": errText(cause), "remoteUrl": url,
	}))
}

// emitSynced uses the agent origin to keep a browser-only badge update off
// Claude's channel, as organization.updated does.
func (s *Syncer) emitSynced(ctx context.Context, reviewID string, version int, fields map[string]any) error {
	_, err := s.append(ctx, &ccevent.Event{
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
		nc.SubjectType = "file"
		return nc
	}
	endSide := c.Side
	if c.EndSide != "" {
		endSide = c.EndSide
	}
	nc.Line, nc.Side = c.EndLine, diffSide(endSide)
	if c.StartLine != c.EndLine {
		startSide := endSide
		if c.StartSide != "" {
			startSide = c.StartSide
		}
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

func replyBody(r store.Reply) string {
	if r.Ask == nil {
		return r.Body
	}
	var b strings.Builder
	b.WriteString(r.Body)
	b.WriteString("\n")
	if r.Ask.Header != "" {
		b.WriteString("\n**" + r.Ask.Header + "**\n")
	}
	b.WriteString("\n")
	for _, o := range r.Ask.Options {
		b.WriteString("- **" + o.Label + "**")
		if o.Description != "" {
			b.WriteString(": " + o.Description)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func remoteID(c github.RemoteComment) string {
	return strconv.FormatInt(c.DatabaseID, 10)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
