package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ccd "github.com/yasyf/cc-interact/daemon"
	ccevent "github.com/yasyf/cc-interact/event"
	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-interact/subject"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/store"
)

// Server is the test harness around the review handlers: it assembles the pieces
// the cc-interact daemon would (the subject resolver, the presence registry, the
// Append chokepoint, the per-repo lock) so each handler can be driven directly
// with a Request and asserted through a Response, without booting a real daemon.
type Server struct {
	cc        *ccstore.Store
	store     *store.Store
	turns     *vcs.TurnStore
	rv        *review
	resolver  subject.Resolver
	activity  *ccd.Activity
	decisions *decisions.Log
	httpPort  int

	repoMu    sync.Mutex
	repoLocks map[string]*sync.Mutex

	injectMu sync.Mutex
	injected []injectCall
}

// injectCall records one solicited-frame injection the review handlers asked
// for, standing in for the daemon's (*ccd.Server).InjectEvent.
type injectCall struct {
	subjectID string
	consumer  string
	pid       int
	payload   string
}

// Request is the test-side view of one control RPC: the envelope identity plus
// every domain field a handler reads from the body.
type Request struct {
	Session       string
	ClaudePID     int
	Cwd           string
	Consumer      string
	New           bool
	Base          string
	Replies       []ReplyInput
	Files         []FileStateInput
	Annotations   []AnnotateInput
	Risk          []string
	Reason        string
	Reviewed      *bool
	Hidden        *bool
	AIRequestID   int64
	AIStatus      string
	Summary       string
	Unmatched     []store.Unmatched
	Question      string
	Ask           *store.Ask
	Organization  *store.Organization
	VersionNumber int
	Partial       bool
	Prompt        string
	ToolName      string
	ToolInput     json.RawMessage
}

// Response is the test-side view of one reply: the envelope outputs plus the
// decoded domain result.
type Response struct {
	OK           bool
	Error        string
	ReviewID     string
	Status       string
	HTTPPort     int
	Allow        bool
	Reason       string
	URL          string
	Version      int
	Resumed      bool
	ChannelState string
	AIRequests   []json.RawMessage
	FeedbackPath string
	Feedback     json.RawMessage
	ReviewFiles  json.RawMessage
	Paths        []string
}

// reviewRow merges a subject with its review_meta for assertions that read the
// pinned base alongside ownership.
type reviewRow struct {
	ID        string
	Status    string
	SessionID string
	ClaudePID int
	BaseRef   string
}

func (req Request) body() json.RawMessage {
	raw, _ := json.Marshal(body{
		New: req.New, Base: req.Base, Replies: req.Replies, Files: req.Files,
		Annotations: req.Annotations, Risk: req.Risk, Reason: req.Reason,
		Reviewed: req.Reviewed, Hidden: req.Hidden,
		AIRequestID: req.AIRequestID, AIStatus: req.AIStatus, Summary: req.Summary,
		Unmatched: req.Unmatched, Question: req.Question, Ask: req.Ask, Organization: req.Organization,
		VersionNumber: req.VersionNumber, Partial: req.Partial, Prompt: req.Prompt,
	})
	return raw
}

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // keep handleStart's ~/.cc-review review dirs out of the real home
	cc, err := ccstore.Open(filepath.Join(t.TempDir(), "t.db"), store.ReviewMigrate)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	ledger, err := decisions.Open(filepath.Join(t.TempDir(), "decisions.db"))
	if err != nil {
		t.Fatalf("open decisions ledger: %v", err)
	}
	t.Cleanup(func() { _ = ledger.Close() })

	// One commit on main; tests write their own pending files, since handleStart
	// fails fast on a repo with nothing to review.
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, "base.go", "package p\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "init")

	s := newServer(cc, ledger)
	return s, repo
}

func newServer(cc *ccstore.Store, ledger *decisions.Log) *Server {
	st := store.New(cc.DB())
	s := &Server{
		cc:        cc,
		store:     st,
		turns:     vcs.NewTurnStore(cc.DB()),
		rv:        &review{decisions: ledger, log: log.New(io.Discard, "", 0)},
		activity:  ccd.NewActivity(),
		decisions: ledger,
		repoLocks: make(map[string]*sync.Mutex),
	}
	s.resolver = subject.Resolver{
		Store: ccstore.NewSubjectStore(cc.DB()),
		Policy: subject.Policy{
			Active: func(sub subject.Subject) bool { return sub.Status == "open" },
		},
	}
	s.rv.injectEvent = func(subjectID, consumer string, pid int, payload string) int {
		s.injectMu.Lock()
		defer s.injectMu.Unlock()
		s.injected = append(s.injected, injectCall{subjectID, consumer, pid, payload})
		return 1
	}
	return s
}

func (s *Server) injectCalls() []injectCall {
	s.injectMu.Lock()
	defer s.injectMu.Unlock()
	return append([]injectCall(nil), s.injected...)
}

// appendEvent mirrors the daemon's Append chokepoint: persist, then publish (no
// bus in tests — handlers only need the row to land for read-back assertions).
func (s *Server) appendEvent(ctx context.Context, e *ccevent.Event) (int64, error) {
	return s.cc.AppendEvent(ctx, e)
}

func (s *Server) repoLock(scope string) *sync.Mutex {
	s.repoMu.Lock()
	defer s.repoMu.Unlock()
	mu, ok := s.repoLocks[scope]
	if !ok {
		mu = &sync.Mutex{}
		s.repoLocks[scope] = mu
	}
	return mu
}

func (s *Server) hc(ctx context.Context, req Request, scope string) ccd.HandlerCtx {
	return ccd.HandlerCtx{
		Ctx:      ctx,
		Env:      ccd.Envelope{Session: req.Session, ClaudePID: req.ClaudePID, Scope: req.Cwd, Consumer: req.Consumer, Body: req.body()},
		Window:   subject.Window{Session: req.Session, ClaudePID: req.ClaudePID},
		Scope:    scope,
		Subjects: s.resolver,
		DB:       s.store.DB(),
		Append:   s.appendEvent,
		HTTPPort: s.httpPort,
		Activity: s.activity,
		RepoLock: s.repoLock(scope),
	}
}

// run resolves the repo scope (as the daemon's ScopeResolve=vcs.Root does) and
// invokes the handler; an unresolvable scope errors, matching the daemon's
// dispatch for a domain op outside a guarded repo.
func (s *Server) run(ctx context.Context, req Request, h func(*review, ccd.HandlerCtx) ccd.Reply) Response {
	root, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return toResponse(h(s.rv, s.hc(ctx, req, root)))
}

func toResponse(reply ccd.Reply) Response {
	var res result
	if len(reply.Body) > 0 {
		_ = json.Unmarshal(reply.Body, &res)
	}
	return Response{
		OK: reply.OK, Error: reply.Error, ReviewID: reply.SubjectID, Status: reply.Status,
		HTTPPort: reply.HTTPPort, Allow: reply.Allow, Reason: reply.Reason,
		URL: res.URL, Version: res.Version, Resumed: res.Resumed, ChannelState: res.ChannelState,
		AIRequests: res.AIRequests, FeedbackPath: res.FeedbackPath, Feedback: res.Feedback,
		ReviewFiles: res.ReviewFiles, Paths: res.Paths,
	}
}

func (s *Server) handleStart(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleStart)
}

func (s *Server) handleFileStates(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleFileStates)
}

func (s *Server) handleUpdateAIRequest(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleUpdateAIRequest)
}

func (s *Server) handleSubmitOrganization(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleSubmitOrganization)
}

func (s *Server) handleFileStatesByRisk(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleFileStatesByRisk)
}

func (s *Server) handleAnnotate(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleAnnotate)
}

func (s *Server) handleReviewFiles(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleReviewFiles)
}

func (s *Server) handleTurnStart(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleTurnStart)
}

func (s *Server) handleTurnEnd(ctx context.Context, req Request) Response {
	return s.run(ctx, req, (*review).handleTurnEnd)
}

// handleReply bypasses scope resolution: the reply handler keys off comment_id,
// not the repo scope, so tests need not supply a Cwd.
func (s *Server) handleReply(ctx context.Context, req Request) Response {
	return toResponse(s.rv.handleReply(s.hc(ctx, req, req.Cwd)))
}

// handleSessionRecord mirrors the daemon's core session-record: rebind the
// window's subject to the rotated session id; a blank session or non-repo is a
// no-op.
func (s *Server) handleSessionRecord(ctx context.Context, req Request) Response {
	if req.Session == "" {
		return Response{OK: true}
	}
	root, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true}
	}
	if err := s.resolver.Rebind(ctx, subject.Window{Session: req.Session, ClaudePID: req.ClaudePID}, root); err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true}
}

// handleResolve mirrors the daemon's core resolve: note the consumer poll and
// look up the window's subject without creating.
func (s *Server) handleResolve(ctx context.Context, req Request) Response {
	root, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	if req.Consumer != "" {
		s.activity.NotePoll(root, req.Consumer, req.ClaudePID)
	}
	resp := Response{OK: true, HTTPPort: s.httpPort}
	sub, ok, err := s.resolver.Find(ctx, subject.Window{Session: req.Session, ClaudePID: req.ClaudePID}, root)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	if ok {
		resp.ReviewID = sub.ID
		resp.Status = sub.Status
	}
	return resp
}

// handleGuardEdit mirrors the daemon's core guard-edit driving cc-review's gate:
// resolve the subject, fail closed on a resolve error, allow when none, else the
// gate verdict plus the decision-ledger observation.
func (s *Server) handleGuardEdit(ctx context.Context, req Request) Response {
	root, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true, Allow: true} // not a repo: nothing to guard
	}
	w := subject.Window{Session: req.Session, ClaudePID: req.ClaudePID}
	sub, ok, err := s.resolver.Find(ctx, w, root)
	if err != nil {
		return Response{OK: true, Allow: false, Reason: gateErrorReason}
	}
	if !ok {
		return Response{OK: true, Allow: true}
	}
	tool := ccd.ToolCall{Name: req.ToolName, Input: req.ToolInput}
	allow, reason := s.rv.gate(ctx, sub, tool)
	s.rv.gateObserve(ctx, sub, tool, allow, reason)
	return Response{OK: true, Allow: allow, Reason: reason}
}

func (s *Server) channelState(reviewID, scope string, pid int) string {
	return channelState(s.activity, reviewID, scope, pid)
}

func (s *Server) sweepStalePending(ctx context.Context, before time.Time) error {
	return s.rv.sweepStalePending(ctx, s.store, s.appendEvent, before)
}

// createReview seeds a review (a cc-interact subject + its review_meta) the way
// handleStart would, for tests that need an existing review without a snapshot.
func (s *Server) createReview(ctx context.Context, session string, pid int, repo, branch, base string) (subject.Subject, error) {
	sub, err := s.resolver.Store.Create(ctx, store.NewSlugHash(), store.ReviewSlug(branch, store.NewSlugHash()), session, repo, pid, "open")
	if err != nil {
		return subject.Subject{}, err
	}
	if err := s.store.SetReviewMeta(ctx, sub.ID, base, branch); err != nil {
		return subject.Subject{}, err
	}
	return sub, nil
}

// getReview returns a subject merged with its pinned base.
func (s *Server) getReview(ctx context.Context, id string) (reviewRow, error) {
	sub, err := s.resolver.Store.Get(ctx, id)
	if err != nil {
		return reviewRow{}, err
	}
	meta, _, err := s.store.GetReviewMeta(ctx, id)
	if err != nil {
		return reviewRow{}, err
	}
	return reviewRow{ID: sub.ID, Status: sub.Status, SessionID: sub.SessionID, ClaudePID: sub.ClaudePID, BaseRef: meta.BaseRef}, nil
}

func (s *Server) reviewStatus(ctx context.Context, id string) (string, error) {
	sub, err := s.resolver.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return sub.Status, nil
}

func seedComment(t *testing.T, s *Server, root string) (reviewID string, commentID int64) {
	t.Helper()
	ctx := context.Background()
	r, err := s.createReview(ctx, "s1", 0, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.store.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]", "")
	if err != nil {
		t.Fatal(err)
	}
	cid, err := s.store.CreateComment(ctx, store.Comment{
		VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "hm",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r.ID, cid
}

func countEvents(t *testing.T, s *Server, reviewID, typ string) int {
	t.Helper()
	events, err := s.cc.EventsSince(context.Background(), reviewID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}
