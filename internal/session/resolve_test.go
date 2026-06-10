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

func TestResolveAdoptsOpenRepoReviewByDefault(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	// An open review bound to another session (e.g. before a session rotation).
	pre, _ := st.CreateReview(ctx, "s1", "/repo")

	r, resumed, err := Resolve(ctx, st, Opts{SessionID: "s2", RepoRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed || r.ID != pre.ID {
		t.Fatalf("default resolve should adopt the open repo review (resumed=%v id=%q)", resumed, r.ID)
	}
	if r.SessionID != "s2" {
		t.Fatalf("adoption should rebind the session id, got %q", r.SessionID)
	}
	got, ok, _ := st.FindReviewBySessionRepo(ctx, "s2", "/repo")
	if !ok || got.ID != pre.ID {
		t.Fatal("rebinding did not persist")
	}
	hist, err := st.ListReviewSessions(ctx, pre.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].Source != "create" || hist[1].Source != "adopt" || hist[1].SessionID != "s2" {
		t.Fatalf("binding history = %+v, want [create:s1, adopt:s2]", hist)
	}
}

func TestResolveExactMatchWinsOverNewerOpenReview(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	mine, _ := st.CreateReview(ctx, "s2", "/repo")
	other, _ := st.CreateReview(ctx, "s1", "/repo")

	r, resumed, err := Resolve(ctx, st, Opts{SessionID: "s2", RepoRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed || r.ID != mine.ID {
		t.Fatalf("exact match should win (resumed=%v id=%q want %q)", resumed, r.ID, mine.ID)
	}
	// The other session's binding must be untouched.
	got, ok, _ := st.FindReviewBySessionRepo(ctx, "s1", "/repo")
	if !ok || got.ID != other.ID {
		t.Fatal("the sibling session's binding was disturbed")
	}
}

func TestResolveSessionlessStartAdoptsWithoutBinding(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pre, _ := st.CreateReview(ctx, "s1", "/repo")

	r, resumed, err := Resolve(ctx, st, Opts{SessionID: "", RepoRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed || r.ID != pre.ID {
		t.Fatalf("sessionless start should still adopt (resumed=%v)", resumed)
	}
	got, _ := st.GetReview(ctx, pre.ID)
	if got.SessionID != "s1" {
		t.Fatalf("sessionless adoption must not rebind, got %q", got.SessionID)
	}
	hist, _ := st.ListReviewSessions(ctx, pre.ID)
	if len(hist) != 1 {
		t.Fatalf("sessionless adoption must not append history, got %d rows", len(hist))
	}
}

func TestResolveCreatesWhenNoOpenReview(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	pre, _ := st.CreateReview(ctx, "s1", "/repo")
	if err := st.SetReviewStatus(ctx, pre.ID, "submitted"); err != nil {
		t.Fatal(err)
	}

	r, resumed, err := Resolve(ctx, st, Opts{SessionID: "s2", RepoRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed || r.ID == pre.ID {
		t.Fatalf("no open review should mean a fresh create (resumed=%v)", resumed)
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
