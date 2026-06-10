package daemon

import (
	"context"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/gitdiff"
	"github.com/yasyf/cc-review/internal/store"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return &Server{store: st, bus: NewBus(), activity: NewActivity(), log: log.New(io.Discard, "", 0)}, repo
}

func seedComment(t *testing.T, s *Server, root string) (reviewID string, commentID int64) {
	t.Helper()
	ctx := context.Background()
	r, err := s.store.CreateReview(ctx, "s1", root, "main")
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
	events, err := s.store.EventsSince(ctx, reviewID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	askEvents := 0
	for _, e := range events {
		if e.Type == store.EventClaudeAsk {
			askEvents++
		}
	}
	if askEvents != 1 {
		t.Fatalf("got %d claude.ask events, want 1 (no re-emit on duplicate)", askEvents)
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
	events, _ := s.store.EventsSince(ctx, reviewID, 0, false)
	updated := 0
	for _, e := range events {
		if e.Type == store.EventCommentUpdated {
			updated++
		}
	}
	if updated != 2 {
		t.Fatalf("got %d comment.updated events, want 2", updated)
	}
}

func repoRoot(t *testing.T, cwd string) string {
	t.Helper()
	root, err := gitdiff.RepoRoot(context.Background(), cwd)
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

func TestSessionRecordReparentsLatestOpenReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	r, _ := s.store.CreateReview(ctx, "s1", root, "main")

	resp := s.handleSessionRecord(ctx, Request{Session: "s2", Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	got, ok, _ := s.store.FindReviewBySessionRepo(ctx, "s2", root)
	if !ok || got.ID != r.ID {
		t.Fatal("review was not reparented to the new session")
	}
	hist, _ := s.store.ListReviewSessions(ctx, r.ID)
	if len(hist) != 2 || hist[1].Source != "session-start" || hist[1].SessionID != "s2" {
		t.Fatalf("history = %+v, want [create:s1, session-start:s2]", hist)
	}
}

func TestSessionRecordSkipsWhenSessionAlreadyBound(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	mine, _ := s.store.CreateReview(ctx, "s2", root, "main")
	other, _ := s.store.CreateReview(ctx, "s1", root, "main")

	resp := s.handleSessionRecord(ctx, Request{Session: "s2", Cwd: repo})
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

	resp := s.handleSessionRecord(ctx, Request{Session: "s1", Cwd: t.TempDir()})
	if !resp.OK {
		t.Fatalf("session-record outside a repo should be OK, got: %s", resp.Error)
	}
	hook, ok, err := s.store.GetSessionHook(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("hook row should still be recorded: ok=%v err=%v", ok, err)
	}
	if hook.SessionID != "s1" {
		t.Fatalf("hook session = %q", hook.SessionID)
	}
}

func TestChannelActiveFromPollAndAttach(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)
	s.startedAt = time.Now().Add(-time.Minute) // long-running daemon: no boot grace

	if s.channelActive(ctx, "r1", "/repo") {
		t.Fatal("no signal must read inactive")
	}
	s.activity.NotePoll("/repo", channelConsumer)
	if !s.channelActive(ctx, "r1", "/repo") {
		t.Fatal("a recent resolve poll must read active")
	}

	s2, _ := testServer(t)
	s2.startedAt = time.Now().Add(-time.Minute)
	detach := s2.activity.Attach("r1", channelConsumer)
	defer detach()
	if !s2.channelActive(ctx, "r1", "/repo") {
		t.Fatal("a live SSE attachment must read active")
	}
}

func TestChannelActiveColdBootGraceCatchesLatePoll(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)
	s.startedAt = time.Now() // cold boot: grace window open

	go func() {
		time.Sleep(300 * time.Millisecond)
		s.activity.NotePoll("/repo", channelConsumer)
	}()
	if !s.channelActive(ctx, "r1", "/repo") {
		t.Fatal("a poll landing inside the boot grace must read active")
	}

	// Past the grace window the answer is immediate, not a 3s wait.
	s.startedAt = time.Now().Add(-time.Minute)
	begin := time.Now()
	if s.channelActive(ctx, "r2", "/other") {
		t.Fatal("no signal must read inactive")
	}
	if elapsed := time.Since(begin); elapsed > 500*time.Millisecond {
		t.Fatalf("inactive answer took %s; must not wait outside the grace window", elapsed)
	}
}

func TestGuardEditBlocksAfterRotation(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	if _, err := s.store.CreateReview(ctx, "s1", root, "main"); err != nil {
		t.Fatal(err)
	}

	// Before reparenting, the rotated session would slip past the guard.
	resp := s.handleSessionRecord(ctx, Request{Session: "s2", Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	guard := s.handleGuardEdit(ctx, Request{Session: "s2", Cwd: repo})
	if guard.Allow {
		t.Fatal("guard-edit must keep blocking after session rotation")
	}
}
