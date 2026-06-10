package daemon

import (
	"context"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"testing"

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
	return &Server{store: st, log: log.New(io.Discard, "", 0)}, repo
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
	r, _ := s.store.CreateReview(ctx, "s1", root)

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
	mine, _ := s.store.CreateReview(ctx, "s2", root)
	other, _ := s.store.CreateReview(ctx, "s1", root)

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

func TestGuardEditBlocksAfterRotation(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	if _, err := s.store.CreateReview(ctx, "s1", root); err != nil {
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
