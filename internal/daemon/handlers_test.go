package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/cc-interact/subject"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/digest"
	"github.com/yasyf/cc-review/internal/store"
)

func gitRun(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...) //nolint:gosec // G204: test helper running git against a test-controlled temp repo with test-controlled args.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
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

	in := ReplyInput{
		CommentID: cid, Kind: "ask", Body: "pick",
		Ask: &store.Ask{Header: "H", Options: []store.AskOption{{Label: "A", Description: "d"}, {Label: "B"}}},
	}
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

	askID, _, err := s.store.CreateReply(ctx, store.Reply{
		CommentID: cid, Origin: "claude", Kind: "ask", Body: "pick",
		Ask: &store.Ask{Options: []store.AskOption{{Label: "A"}, {Label: "B"}}},
	})
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
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

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
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}

	// Same pid, rotated session id: the binding follows the window.
	resp := s.handleSessionRecord(ctx, Request{Session: "sB", ClaudePID: 100, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	got, ok, _ := s.resolver.Store.FindBySessionScope(ctx, "sB", root)
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
	orphan, err := s.createReview(ctx, "sA", 100, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}

	// pid 100 is dead (default alive: nothing lives): the new window adopts.
	resp := s.handleSessionRecord(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	got, ok, _ := s.resolver.Store.FindBySessionScope(ctx, "sB", root)
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
	held, err := s.createReview(ctx, "sA", 100, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}

	resp := s.handleSessionRecord(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	got, err := s.getReview(ctx, held.ID)
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
	mine, _ := s.createReview(ctx, "s2", 200, root, "main", "base0")
	other, _ := s.createReview(ctx, "s1", 100, root, "main", "base0")

	resp := s.handleSessionRecord(ctx, Request{Session: "s2", ClaudePID: 200, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	// Neither binding moves: s2 keeps its own review, s1 keeps the newer one.
	got, ok, _ := s.resolver.Store.FindBySessionScope(ctx, "s2", root)
	if !ok || got.ID != mine.ID {
		t.Fatal("s2's own binding was disturbed")
	}
	got, ok, _ = s.resolver.Store.FindBySessionScope(ctx, "s1", root)
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
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	orphan, err := s.createReview(ctx, "sA", 100, root, "main", head)
	if err != nil {
		t.Fatal(err)
	}
	// A competing window wins the adoption between the loser's peek and the
	// write phase; by the write phase the winner's window is alive.
	stole := false
	s.resolver.Policy.Held = func(ctx context.Context, sub subject.Subject) bool {
		if stole {
			return true
		}
		stole = true
		if ok, err := s.resolver.Store.Rebind(ctx, sub.ID, sub.ClaudePID, "winner", 999); err != nil || !ok {
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
	got, err := s.getReview(ctx, orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "winner" || got.ClaudePID != 999 {
		t.Fatalf("winner's binding disturbed: %s/%d", got.SessionID, got.ClaudePID)
	}
}

func TestHandleStartNoChanges(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)

	// A clean tree sitting on trunk has nothing to review, even via the
	// fallback; nothing may be created.
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, "a.go", "package a\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "init")

	resp := s.handleStart(ctx, Request{Session: "sN", ClaudePID: 100, Cwd: repo})
	if resp.OK {
		t.Fatal("start on a clean trunk must fail")
	}
	if !strings.Contains(resp.Error, "no changes to capture") {
		t.Fatalf("error = %q, want it to name no changes to capture", resp.Error)
	}
	if _, ok, err := s.resolver.Store.FindBySessionScope(ctx, "sN", repoRoot(t, repo)); err != nil || ok {
		t.Fatalf("a failed start created a review (ok=%v err=%v)", ok, err)
	}
}

func TestHandleStartTrunkFallbackAndExplicitBase(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)
	s.alive = aliveSet(100, 200)

	// Commit A on main, commit B on feature, clean tree.
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, "trunk.go", "package a\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "A")
	shaA := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	gitRun(t, repo, "checkout", "-qb", "feature")
	writeFile(t, repo, "branch.go", "package a\nvar B int\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "B")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo, Base: "main"})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	review, err := s.getReview(ctx, started.ReviewID)
	if err != nil || review.BaseRef != shaA {
		t.Fatalf("pinned base = %q (err %v), want %q", review.BaseRef, err, shaA)
	}
	v, ok, err := s.store.LatestVersion(ctx, started.ReviewID)
	if err != nil || !ok || v.BaseRef != shaA {
		t.Fatalf("version base = %q (ok=%v err=%v), want %q", v.BaseRef, ok, err, shaA)
	}

	// --base against an existing review is rejected; --new re-pins.
	if resp := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo, Base: "main"}); resp.OK || !strings.Contains(resp.Error, "--new") {
		t.Fatalf("ok=%v err=%q, want a rejection pointing at --new", resp.OK, resp.Error)
	}

	// The trunk fallback pins the same fork point without --base.
	fallback := s.handleStart(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo, New: true})
	if !fallback.OK {
		t.Fatalf("fallback start: %s", fallback.Error)
	}
	review, err = s.getReview(ctx, fallback.ReviewID)
	if err != nil || review.BaseRef != shaA {
		t.Fatalf("fallback pinned base = %q (err %v), want trunk fork point %q", review.BaseRef, err, shaA)
	}
}

func TestHandleStartPinnedBaseAndDedupAcrossCommits(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.alive = aliveSet(100)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	review, err := s.getReview(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}

	// Committing the pending work moves HEAD but not the pinned base, so the
	// recaptured patch is byte-identical and the version is reused.
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "pending landed")
	resumed := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !resumed.OK || !resumed.Resumed {
		t.Fatalf("resume: ok=%v resumed=%v err=%q", resumed.OK, resumed.Resumed, resumed.Error)
	}
	if resumed.Version != started.Version {
		t.Fatalf("identical snapshot made version %d, want reuse of %d", resumed.Version, started.Version)
	}
	if got := countEvents(t, s, started.ReviewID, store.EventVersionCreated); got != 1 {
		t.Fatalf("version.created events = %d, want 1", got)
	}
	if got := countEvents(t, s, started.ReviewID, store.EventAIRequestCreated); got != 1 {
		t.Fatalf("ai.request.created events = %d, want 1 (dedup must not re-organize)", got)
	}

	// New work lands a new version that still diffs from the pinned base, so
	// the committed file stays in the review.
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\nvar More int\n")
	again := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !again.OK || again.Version != started.Version+1 {
		t.Fatalf("second version: ok=%v version=%d err=%q, want %d", again.OK, again.Version, again.Error, started.Version+1)
	}
	v, ok, err := s.store.LatestVersion(ctx, started.ReviewID)
	if err != nil || !ok {
		t.Fatalf("latest version: ok=%v err=%v", ok, err)
	}
	if v.BaseRef != review.BaseRef {
		t.Fatalf("v2 base = %q, want pinned %q", v.BaseRef, review.BaseRef)
	}
	patch, err := os.ReadFile(v.PatchPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"var Pending int", "var More int"} {
		if !strings.Contains(string(patch), want) {
			t.Fatalf("v2 patch missing %q:\n%s", want, patch)
		}
	}
}

func TestHandleStartDedupReopensSubmittedReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.alive = aliveSet(100)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	if err := s.resolver.Store.SetStatus(ctx, started.ReviewID, "submitted"); err != nil {
		t.Fatal(err)
	}

	resumed := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !resumed.OK || !resumed.Resumed || resumed.Version != started.Version {
		t.Fatalf("resume: ok=%v resumed=%v version=%d err=%q, want dedup of version %d", resumed.OK, resumed.Resumed, resumed.Version, resumed.Error, started.Version)
	}
	if status, _ := s.reviewStatus(ctx, started.ReviewID); status != "open" {
		t.Fatalf("status = %q, want open: a successful start must reopen the round", status)
	}
	if guard := s.handleGuardEdit(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo}); guard.Allow {
		t.Fatal("edit guard must block after a dedup resume of a submitted review")
	}
}

func TestHandleStartRaceCreateUsesCreateSemantics(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)

	// Orphan pinned to the current HEAD, then HEAD advances past it.
	headA := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	orphan, err := s.createReview(ctx, "sA", 100, root, "main", headA)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "landed.go", "package p\nvar Landed int\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "landed")
	headB := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	// The orphan is stolen between the loser's peek and the write phase.
	stole := false
	s.resolver.Policy.Held = func(ctx context.Context, sub subject.Subject) bool {
		if stole {
			return true
		}
		stole = true
		if ok, err := s.resolver.Store.Rebind(ctx, sub.ID, sub.ClaudePID, "winner", 999); err != nil || !ok {
			t.Fatalf("competing rebind: ok=%v err=%v", ok, err)
		}
		return false
	}

	resp := s.handleStart(ctx, Request{Session: "loser", ClaudePID: 300, Cwd: repo})
	if !resp.OK || resp.Resumed || resp.ReviewID == orphan.ID {
		t.Fatalf("loser must create its own: ok=%v resumed=%v id=%q err=%q", resp.OK, resp.Resumed, resp.ReviewID, resp.Error)
	}
	review, err := s.getReview(ctx, resp.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if review.BaseRef != headB {
		t.Fatalf("created review pinned to %q, want create-semantics HEAD %q (not the stolen orphan's %q)", review.BaseRef, headB, headA)
	}
	v, ok, err := s.store.LatestVersion(ctx, resp.ReviewID)
	if err != nil || !ok {
		t.Fatalf("latest version: ok=%v err=%v", ok, err)
	}
	patch, err := os.ReadFile(v.PatchPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "pending.go") || strings.Contains(string(patch), "landed.go") {
		t.Fatalf("patch must be the session diff (pending.go only):\n%s", patch)
	}
}

func TestGuardEditPerWindow(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	s.alive = aliveSet(100, 200)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

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

func TestGuardEditWritesGateDecisions(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	r, err := s.createReview(ctx, "s1", 100, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}

	blockedInput := json.RawMessage(`{"file_path":"` + filepath.Join(root, "a.go") + `","old_string":"a","new_string":"b"}`)
	if resp := s.handleGuardEdit(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, ToolName: "Edit", ToolInput: blockedInput}); resp.Allow {
		t.Fatal("guard-edit must block while the review is open")
	}
	if err := s.resolver.Store.SetStatus(ctx, r.ID, "submitted"); err != nil {
		t.Fatal(err)
	}
	allowedInput := json.RawMessage(`{"file_path":"` + filepath.Join(root, "b.go") + `","content":"package p\n"}`)
	if resp := s.handleGuardEdit(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, ToolName: "Write", ToolInput: allowedInput}); !resp.Allow {
		t.Fatal("guard-edit must allow once submitted")
	}

	rows, err := s.decisions.ForTurn("s1", 0, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("decisions = %d, want a block then an allow", len(rows))
	}
	for i, want := range []struct {
		action, tool, file, message string
		input                       json.RawMessage
	}{
		{action: "block", tool: "Edit", file: filepath.Join(root, "a.go"), message: "cc-review: an open review is awaiting your feedback — edits are blocked until you press Submit in the browser.", input: blockedInput},
		{action: "allow", tool: "Write", file: filepath.Join(root, "b.go"), input: allowedInput},
	} {
		row := rows[i]
		if row.Source != "cc-review" || row.Kind != "gate" || row.Event != "PreToolUse" {
			t.Fatalf("row %d = %+v, want a cc-review gate PreToolUse row", i, row)
		}
		if row.Action != want.action || row.ToolName != want.tool || row.Message != want.message {
			t.Fatalf("row %d = %s %s %q, want %s %s %q", i, row.Action, row.ToolName, row.Message, want.action, want.tool, want.message)
		}
		wantDigest, err := digest.Tool(want.tool, want.input)
		if err != nil {
			t.Fatal(err)
		}
		if row.ToolDigest != wantDigest {
			t.Fatalf("row %d digest = %q, want %q", i, row.ToolDigest, wantDigest)
		}
		var detail struct {
			FilePath string `json:"file_path"`
			ReviewID string `json:"review_id"`
		}
		if err := json.Unmarshal([]byte(row.DetailJSON), &detail); err != nil {
			t.Fatalf("row %d detail %q: %v", i, row.DetailJSON, err)
		}
		if detail.FilePath != want.file || detail.ReviewID != r.ID {
			t.Fatalf("row %d detail = %+v, want file %s review %s", i, detail, want.file, r.ID)
		}
	}
}

func TestResolveStaleSessionFindsOwnWindowReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	a, _ := s.createReview(ctx, "sA", 100, root, "main", "base0")
	if _, err := s.createReview(ctx, "sB", 200, root, "main", "base0"); err != nil {
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

func TestChannelState(t *testing.T) {
	// The window under test is pid 100; a non-zero pid names which window each
	// signal belongs to, so foreign-pid rows prove the keying.
	for _, tc := range []struct {
		name                          string
		provenPID, attachPID, pollPID int // 0 = no signal
		want                          string
	}{
		{"no signal", 0, 0, 0, "inactive"},
		{"poll only", 0, 0, 100, "pending"},
		{"attach only", 0, 100, 0, "pending"},
		{"attach and poll unproven", 0, 100, 100, "pending"},
		{"proven without presence", 100, 0, 0, "inactive"},
		{"proven with poll only", 100, 0, 100, "pending"},
		{"proven and attached", 100, 100, 0, "active"},
		{"proven attached and polled", 100, 100, 100, "active"},
		{"foreign window's poll", 0, 0, 200, "inactive"},
		{"foreign window's attachment", 0, 200, 0, "inactive"},
		{"foreign window's proof while attached", 200, 100, 0, "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{activity: ccd.NewActivity()}
			if tc.provenPID != 0 {
				s.activity.MarkProven(tc.provenPID)
			}
			if tc.attachPID != 0 {
				detach := s.activity.Attach("r1", channelConsumer, tc.attachPID)
				defer detach()
			}
			if tc.pollPID != 0 {
				s.activity.NotePoll("/repo", channelConsumer, tc.pollPID)
			}
			if got := s.channelState("r1", "/repo", 100); got != tc.want {
				t.Fatalf("channelState = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleChannelAck(t *testing.T) {
	s, repo := testServer(t)

	// An attached-but-unproven window flips pending -> active once its channel
	// round trip is proven (what channel-ack records via Activity.MarkProven).
	detach := s.activity.Attach("r1", channelConsumer, 100)
	defer detach()
	if got := s.channelState("r1", repo, 100); got != "pending" {
		t.Fatalf("pre-ack state = %q, want pending", got)
	}
	s.activity.MarkProven(100)
	if got := s.channelState("r1", repo, 100); got != "active" {
		t.Fatalf("post-ack state = %q, want active", got)
	}
	if got := s.channelState("r1", repo, 200); got == "active" {
		t.Fatal("the ack must not prove another window")
	}
}
