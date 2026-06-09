package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yasyf/cc-review/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestResolveCreatesThenResumes(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	o := Opts{SessionID: "s1", RepoRoot: "/repo"}

	r1, resumed, err := Resolve(ctx, st, o)
	if err != nil || resumed {
		t.Fatalf("first resolve: resumed=%v err=%v", resumed, err)
	}
	r2, resumed, err := Resolve(ctx, st, o)
	if err != nil || !resumed {
		t.Fatalf("second resolve: resumed=%v err=%v", resumed, err)
	}
	if r1.ID != r2.ID {
		t.Fatalf("resume returned a different review: %q vs %q", r1.ID, r2.ID)
	}
}

func TestResolveNoSilentRepoAdoption(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	// A session-less open review exists for the repo (e.g. from a prior fallback).
	pre, _ := st.CreateReview(ctx, "", "/repo")

	// A new session with no flags must NOT silently adopt it.
	r, resumed, err := Resolve(ctx, st, Opts{SessionID: "s1", RepoRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed || r.ID == pre.ID {
		t.Fatalf("default resolve silently adopted the repo-root review (resumed=%v)", resumed)
	}
}

func TestResolveExplicitResumeAdopts(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pre, _ := st.CreateReview(ctx, "", "/repo")

	r, resumed, err := Resolve(ctx, st, Opts{SessionID: "s1", RepoRoot: "/repo", Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed || r.ID != pre.ID {
		t.Fatalf("--resume should adopt the open repo-root review (resumed=%v id=%q)", resumed, r.ID)
	}
	if r.SessionID != "s1" {
		t.Fatalf("adoption should backfill the session id, got %q", r.SessionID)
	}
	// The backfill must persist and now be an exact match.
	got, ok, _ := st.FindReviewBySessionRepo(ctx, "s1", "/repo")
	if !ok || got.ID != pre.ID {
		t.Fatalf("backfilled session id did not persist")
	}
}

func TestResolveNewDetachesAndCreates(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	old, _, err := Resolve(ctx, st, Opts{SessionID: "s1", RepoRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}

	fresh, resumed, err := Resolve(ctx, st, Opts{SessionID: "s1", RepoRoot: "/repo", New: true})
	if err != nil {
		t.Fatalf("--new resolve: %v", err)
	}
	if resumed || fresh.ID == old.ID {
		t.Fatalf("--new should create a fresh review, got resumed=%v same=%v", resumed, fresh.ID == old.ID)
	}
	// The old review is closed and session-detached; the new one owns the slot.
	oldStatus, _ := st.ReviewStatus(ctx, old.ID)
	if oldStatus != "closed" {
		t.Fatalf("old review status = %q, want closed", oldStatus)
	}
	match, ok, _ := st.FindReviewBySessionRepo(ctx, "s1", "/repo")
	if !ok || match.ID != fresh.ID {
		t.Fatalf("the fresh review should now own (s1,/repo)")
	}
}
