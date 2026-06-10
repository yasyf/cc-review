package daemon

import (
	"context"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/session"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/vcs"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // keep handleStart's ~/.cc-review review dirs out of the real home
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	s := &Server{
		store:    st,
		bus:      NewBus(),
		activity: NewActivity(),
		alive:    func(int) bool { return false },
		log:      log.New(io.Discard, "", 0),
	}
	s.resolver = session.Resolver{Store: st, Held: s.held}
	return s, repo
}

func aliveSet(pids ...int) func(int) bool {
	return func(pid int) bool {
		for _, p := range pids {
			if p == pid {
				return true
			}
		}
		return false
	}
}

func repoRoot(t *testing.T, cwd string) string {
	t.Helper()
	root, err := vcs.Root(context.Background(), cwd)
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

func seedComment(t *testing.T, s *Server, root string) (reviewID string, commentID int64) {
	t.Helper()
	ctx := context.Background()
	r, err := s.store.CreateReview(ctx, "s1", 0, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.store.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")
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
	events, err := s.store.EventsSince(context.Background(), reviewID, 0, false)
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

func TestDispatchRejectsSkewedProto(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.triggerShutdown = func() {}

	for _, tc := range []struct {
		name   string
		req    Request
		wantOK bool
	}{
		{"health answers any proto", Request{Proto: 1, Op: OpHealth}, true},
		{"shutdown answers any proto", Request{Proto: 1, Op: OpShutdown}, true},
		{"status rejects skew", Request{Proto: 1, Op: OpStatus, Cwd: repo}, false},
		{"status accepts current proto", Request{Proto: ProtocolVersion, Op: OpStatus, Cwd: repo}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := s.dispatch(ctx, tc.req)
			if resp.OK != tc.wantOK {
				t.Fatalf("ok=%v (err=%q), want %v", resp.OK, resp.Error, tc.wantOK)
			}
			if !tc.wantOK && !strings.Contains(resp.Error, "retry") {
				t.Fatalf("skew error %q must tell the user to retry", resp.Error)
			}
		})
	}
}

func TestHandleReplyValidatesKind(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	_, cid := seedComment(t, s, repoRoot(t, repo))

	ask := &store.Ask{Options: []store.AskOption{{Label: "A"}, {Label: "B"}}}
	for _, tc := range []struct {
		name   string
		in     ReplyInput
		wantOK bool
	}{
		{"clarification ok", ReplyInput{CommentID: cid, Kind: "clarification", Body: "fyi"}, true},
		{"ask ok", ReplyInput{CommentID: cid, Kind: "ask", Body: "pick", Ask: ask}, true},
		{"unknown kind", ReplyInput{CommentID: cid, Kind: "option", Body: "x"}, false},
		{"empty kind", ReplyInput{CommentID: cid, Body: "x"}, false},
		{"ask without payload", ReplyInput{CommentID: cid, Kind: "ask", Body: "x"}, false},
		{"ask without body", ReplyInput{CommentID: cid, Kind: "ask", Ask: ask}, false},
		{"question with payload", ReplyInput{CommentID: cid, Kind: "question", Body: "x", Ask: ask}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := s.handleReply(ctx, Request{Replies: []ReplyInput{tc.in}})
			if resp.OK != tc.wantOK {
				t.Fatalf("ok=%v (err=%q), want %v", resp.OK, resp.Error, tc.wantOK)
			}
		})
	}
}

func TestHandleReplyDedupsEquivalentAsks(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	reviewID, cid := seedComment(t, s, repoRoot(t, repo))

	in := ReplyInput{CommentID: cid, Kind: "ask", Body: "pick",
		Ask: &store.Ask{Header: "H", Options: []store.AskOption{{Label: "A", Description: "d"}, {Label: "B"}}}}
	for i := 0; i < 2; i++ {
		if resp := s.handleReply(ctx, Request{Replies: []ReplyInput{in}}); !resp.OK {
			t.Fatalf("reply %d: %s", i, resp.Error)
		}
	}

	replies, err := s.store.ListRepliesByComment(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1 (redelivery deduped)", len(replies))
	}
	if got := countEvents(t, s, reviewID, store.EventClaudeAsk); got != 1 {
		t.Fatalf("got %d claude.ask events, want 1 (no re-emit on duplicate)", got)
	}
}

func TestHandleAnswerRoutesByTargetKind(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	reviewID, cid := seedComment(t, s, repoRoot(t, repo))

	askID, _, err := s.store.CreateReply(ctx, store.Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "pick",
		Ask: &store.Ask{Options: []store.AskOption{{Label: "A"}, {Label: "B"}}}})
	if err != nil {
		t.Fatal(err)
	}
	qID, _, err := s.store.CreateReply(ctx, store.Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "why?"})
	if err != nil {
		t.Fatal(err)
	}

	// Wrong answer shape for the target kind is rejected.
	if resp := s.handleReply(ctx, Request{Replies: []ReplyInput{{AnswerTo: askID, Answer: "text"}}}); resp.OK {
		t.Fatal("plain answer against an ask must fail")
	}
	if resp := s.handleReply(ctx, Request{Replies: []ReplyInput{{AnswerTo: qID, AskAnswer: &store.AskAnswer{Selected: []string{"A"}}}}}); resp.OK {
		t.Fatal("ask_answer against a question must fail")
	}

	resp := s.handleReply(ctx, Request{Replies: []ReplyInput{
		{AnswerTo: askID, AskAnswer: &store.AskAnswer{Selected: []string{"B"}, Notes: "n"}},
		{AnswerTo: qID, Answer: "because"},
	}})
	if !resp.OK {
		t.Fatalf("answers failed: %s", resp.Error)
	}

	gotAsk, _ := s.store.GetReply(ctx, askID)
	if !gotAsk.Answered || gotAsk.AskAnswer == nil || gotAsk.AskAnswer.Selected[0] != "B" || gotAsk.AnsweredVia != "askuserquestion" {
		t.Fatalf("ask answer = %+v via %q", gotAsk.AskAnswer, gotAsk.AnsweredVia)
	}
	gotQ, _ := s.store.GetReply(ctx, qID)
	if !gotQ.Answered || gotQ.Answer != "because" {
		t.Fatalf("question answer = %q", gotQ.Answer)
	}

	// Each drain answer emits comment.updated so an open browser converges.
	if got := countEvents(t, s, reviewID, store.EventCommentUpdated); got != 2 {
		t.Fatalf("got %d comment.updated events, want 2", got)
	}
}

func TestStartTwoLiveWindowsGetSeparateReviews(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.alive = aliveSet(100, 200)

	a := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	b := s.handleStart(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !a.OK || !b.OK {
		t.Fatalf("start failed: a=%q b=%q", a.Error, b.Error)
	}
	if a.ReviewID == b.ReviewID {
		t.Fatal("two live windows shared one review")
	}
	if b.Resumed {
		t.Fatal("window B must create its own review, not adopt A's")
	}

	// Comments and events stay isolated per review: a reply under A's comment
	// never lands in B's log.
	v, ok, err := s.store.LatestVersion(ctx, a.ReviewID)
	if err != nil || !ok {
		t.Fatalf("latest version: ok=%v err=%v", ok, err)
	}
	cid, err := s.store.CreateComment(ctx, store.Comment{
		VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "hm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp := s.handleReply(ctx, Request{Replies: []ReplyInput{{CommentID: cid, Kind: "clarification", Body: "fyi"}}}); !resp.OK {
		t.Fatalf("reply: %s", resp.Error)
	}
	if got := countEvents(t, s, a.ReviewID, store.EventClaudeClarification); got != 1 {
		t.Fatalf("review A has %d clarification events, want 1", got)
	}
	if got := countEvents(t, s, b.ReviewID, store.EventClaudeClarification); got != 0 {
		t.Fatalf("review B has %d clarification events, want 0 (leaked across windows)", got)
	}
}

func TestSessionRecordRotationFollowsWindow(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.alive = aliveSet(100)
	root := repoRoot(t, repo)

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}

	// Same pid, rotated session id: the binding follows the window.
	resp := s.handleSessionRecord(ctx, Request{Session: "sB", ClaudePID: 100, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	got, ok, _ := s.store.FindReviewBySessionRepo(ctx, "sB", root)
	if !ok || got.ID != started.ReviewID {
		t.Fatal("binding did not follow the rotated session id")
	}

	// Guard-edit still blocks under the new session id.
	guard := s.handleGuardEdit(ctx, Request{Session: "sB", ClaudePID: 100, Cwd: repo})
	if guard.Allow {
		t.Fatal("guard-edit must keep blocking after session rotation")
	}
}

func TestSessionRecordAdoptsDeadWindowsReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	orphan, err := s.store.CreateReview(ctx, "sA", 100, root, "main")
	if err != nil {
		t.Fatal(err)
	}

	// pid 100 is dead (default alive: nothing lives): the new window adopts.
	resp := s.handleSessionRecord(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	got, ok, _ := s.store.FindReviewBySessionRepo(ctx, "sB", root)
	if !ok || got.ID != orphan.ID {
		t.Fatal("orphaned review was not adopted")
	}
	if got.ClaudePID != 200 {
		t.Fatalf("adopted review pid = %d, want 200", got.ClaudePID)
	}
}

func TestSessionRecordNeverStealsLiveWindow(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.alive = aliveSet(100)
	root := repoRoot(t, repo)
	held, err := s.store.CreateReview(ctx, "sA", 100, root, "main")
	if err != nil {
		t.Fatal(err)
	}

	resp := s.handleSessionRecord(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	got, err := s.store.GetReview(ctx, held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "sA" || got.ClaudePID != 100 {
		t.Fatalf("live foreign window's review reparented to %s/%d", got.SessionID, got.ClaudePID)
	}
}

func TestSessionRecordSkipsWhenSessionAlreadyBound(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	mine, _ := s.store.CreateReview(ctx, "s2", 200, root, "main")
	other, _ := s.store.CreateReview(ctx, "s1", 100, root, "main")

	resp := s.handleSessionRecord(ctx, Request{Session: "s2", ClaudePID: 200, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	// Neither binding moves: s2 keeps its own review, s1 keeps the newer one.
	got, ok, _ := s.store.FindReviewBySessionRepo(ctx, "s2", root)
	if !ok || got.ID != mine.ID {
		t.Fatal("s2's own binding was disturbed")
	}
	got, ok, _ = s.store.FindReviewBySessionRepo(ctx, "s1", root)
	if !ok || got.ID != other.ID {
		t.Fatal("s1's binding was stolen despite s2 being bound")
	}
}

func TestSessionRecordOutsideRepoIsNoop(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)

	resp := s.handleSessionRecord(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: t.TempDir()})
	if !resp.OK {
		t.Fatalf("session-record outside a repo should be OK, got: %s", resp.Error)
	}
}

func TestStartAdoptRaceLoserCreatesOwn(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	orphan, err := s.store.CreateReview(ctx, "sA", 100, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	// A competing window wins the adoption between the Held check and the CAS.
	s.resolver.Held = func(ctx context.Context, r store.Review) bool {
		if ok, err := s.store.RebindReview(ctx, r.ID, r.ClaudePID, "winner", 999); err != nil || !ok {
			t.Fatalf("competing rebind: ok=%v err=%v", ok, err)
		}
		return false
	}

	resp := s.handleStart(ctx, Request{Session: "loser", ClaudePID: 300, Cwd: repo})
	if !resp.OK {
		t.Fatalf("start: %s", resp.Error)
	}
	if resp.ReviewID == orphan.ID || resp.Resumed {
		t.Fatalf("loser must create its own review: id=%q resumed=%v", resp.ReviewID, resp.Resumed)
	}
	got, err := s.store.GetReview(ctx, orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "winner" || got.ClaudePID != 999 {
		t.Fatalf("winner's binding disturbed: %s/%d", got.SessionID, got.ClaudePID)
	}
}

func TestGuardEditPerWindow(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.alive = aliveSet(100, 200)

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}

	if guard := s.handleGuardEdit(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo}); guard.Allow {
		t.Fatal("window A must be blocked by its own open review")
	}
	if guard := s.handleGuardEdit(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo}); !guard.Allow {
		t.Fatalf("window B has no review and must be allowed: %s", guard.Reason)
	}
}

func TestResolveStaleSessionFindsOwnWindowReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	a, _ := s.store.CreateReview(ctx, "sA", 100, root, "main")
	if _, err := s.store.CreateReview(ctx, "sB", 200, root, "main"); err != nil {
		t.Fatal(err)
	}

	// A channel server outliving session rotation holds a stale id; the pid
	// still finds the window's own review.
	resp := s.handleResolve(ctx, Request{Session: "stale", ClaudePID: 100, Cwd: repo, Consumer: "channel"})
	if !resp.OK {
		t.Fatalf("resolve: %s", resp.Error)
	}
	if resp.ReviewID != a.ID {
		t.Fatalf("resolved %q, want window 100's own review %q", resp.ReviewID, a.ID)
	}

	// An unknown window never gets another window's review (no repo fallback).
	resp = s.handleResolve(ctx, Request{Session: "stale", ClaudePID: 999, Cwd: repo, Consumer: "channel"})
	if !resp.OK {
		t.Fatalf("resolve: %s", resp.Error)
	}
	if resp.ReviewID != "" {
		t.Fatalf("unknown window resolved %q, want none", resp.ReviewID)
	}
}

func TestChannelActiveIsPIDKeyed(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)
	s.startedAt = time.Now().Add(-time.Minute) // long-running daemon: no boot grace

	if s.channelActive(ctx, "r1", "/repo", 100) {
		t.Fatal("no signal must read inactive")
	}
	s.activity.NotePoll("/repo", channelConsumer, 100)
	if !s.channelActive(ctx, "r1", "/repo", 100) {
		t.Fatal("the window's own resolve poll must read active")
	}
	if s.channelActive(ctx, "r1", "/repo", 200) {
		t.Fatal("window A's polls must not light up window B's start")
	}

	s2, _ := testServer(t)
	s2.startedAt = time.Now().Add(-time.Minute)
	detach := s2.activity.Attach("r1", channelConsumer, 100)
	defer detach()
	if !s2.channelActive(ctx, "r1", "/repo", 100) {
		t.Fatal("a live SSE attachment must read active")
	}
	if s2.channelActive(ctx, "r1", "/repo", 200) {
		t.Fatal("an attachment must not count for another window")
	}
}

func TestChannelActiveColdBootGraceCatchesLatePoll(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)
	s.startedAt = time.Now() // cold boot: grace window open

	go func() {
		time.Sleep(300 * time.Millisecond)
		s.activity.NotePoll("/repo", channelConsumer, 100)
	}()
	if !s.channelActive(ctx, "r1", "/repo", 100) {
		t.Fatal("a poll landing inside the boot grace must read active")
	}

	// Past the grace window the answer is immediate, not a 3s wait.
	s.startedAt = time.Now().Add(-time.Minute)
	begin := time.Now()
	if s.channelActive(ctx, "r2", "/other", 100) {
		t.Fatal("no signal must read inactive")
	}
	if elapsed := time.Since(begin); elapsed > 500*time.Millisecond {
		t.Fatalf("inactive answer took %s; must not wait outside the grace window", elapsed)
	}
}
