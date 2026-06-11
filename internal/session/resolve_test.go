package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yasyf/cc-review/internal/store"
)

const repo = "/repo"

func newResolver(t *testing.T, alive map[int]bool) Resolver {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return Resolver{
		Store: st,
		Held:  func(_ context.Context, r store.Review) bool { return alive[r.ClaudePID] },
	}
}

func seedReview(t *testing.T, ctx context.Context, st *store.Store, sessionID string, pid int, status string) store.Review {
	t.Helper()
	r, err := st.CreateReview(ctx, sessionID, pid, repo, "main", "base0")
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}
	if status != "open" {
		if err := st.SetReviewStatus(ctx, r.ID, status); err != nil {
			t.Fatalf("seed status: %v", err)
		}
		r.Status = status
	}
	return r
}

func bindingOf(t *testing.T, ctx context.Context, st *store.Store, id string) (string, int) {
	t.Helper()
	r, err := st.GetReview(ctx, id)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	return r.SessionID, r.ClaudePID
}

func TestStart(t *testing.T) {
	cases := []struct {
		name        string
		alive       map[int]bool
		seed        func(t *testing.T, ctx context.Context, rs Resolver) store.Review
		w           Window
		fresh       bool
		wantResumed bool
		wantSeeded  bool
		after       func(t *testing.T, ctx context.Context, st *store.Store, seeded, got store.Review)
	}{
		{
			name: "no review creates one bound to the window",
			w:    Window{SessionID: "s1", ClaudePID: 100},
			after: func(t *testing.T, ctx context.Context, st *store.Store, _, got store.Review) {
				if got.SessionID != "s1" || got.ClaudePID != 100 || got.Status != "open" {
					t.Fatalf("created session=%q pid=%d status=%q, want s1/100/open", got.SessionID, got.ClaudePID, got.Status)
				}
				if r, err := st.GetReview(ctx, got.ID); err != nil || r.BaseRef != "base0" {
					t.Fatalf("persisted base = %q (err %v), want base0", r.BaseRef, err)
				}
			},
		},
		{
			name: "same window resumes its review",
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				r, resumed, err := rs.Start(ctx, Window{SessionID: "s1", ClaudePID: 100}, repo, "main", "base0", false)
				if err != nil || resumed {
					t.Fatalf("seed start: resumed=%v err=%v", resumed, err)
				}
				return r
			},
			w:           Window{SessionID: "s1", ClaudePID: 100},
			wantResumed: true,
			wantSeeded:  true,
		},
		{
			name:  "second live window creates its own, first binding untouched",
			alive: map[int]bool{100: true},
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "sA", 100, "open")
			},
			w: Window{SessionID: "sB", ClaudePID: 200},
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, _ store.Review) {
				if sess, pid := bindingOf(t, ctx, st, seeded.ID); sess != "sA" || pid != 100 {
					t.Fatalf("first binding disturbed: %s/%d", sess, pid)
				}
			},
		},
		{
			name: "rotation: new session id same pid rebinds and resumes",
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "sA", 100, "open")
			},
			w:           Window{SessionID: "sB", ClaudePID: 100},
			wantResumed: true,
			wantSeeded:  true,
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, got store.Review) {
				if got.SessionID != "sB" {
					t.Fatalf("returned session = %q, want sB", got.SessionID)
				}
				if _, ok, _ := st.FindReviewBySessionRepo(ctx, "sA", repo); ok {
					t.Fatal("old session id still bound")
				}
				if r, ok, _ := st.FindReviewBySessionRepo(ctx, "sB", repo); !ok || r.ID != seeded.ID {
					t.Fatal("new session id not bound to the review")
				}
			},
		},
		{
			name: "exact session match with stale pid is refreshed",
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "s1", 0, "open")
			},
			w:           Window{SessionID: "s1", ClaudePID: 999},
			wantResumed: true,
			wantSeeded:  true,
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, got store.Review) {
				if got.ClaudePID != 999 {
					t.Fatalf("returned pid = %d, want 999", got.ClaudePID)
				}
				if sess, pid := bindingOf(t, ctx, st, seeded.ID); sess != "s1" || pid != 999 {
					t.Fatalf("binding = %s/%d, want s1/999", sess, pid)
				}
			},
		},
		{
			name: "submitted window review resumes after rotation",
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "sA", 100, "submitted")
			},
			w:           Window{SessionID: "sB", ClaudePID: 100},
			wantResumed: true,
			wantSeeded:  true,
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, _ store.Review) {
				if sess, pid := bindingOf(t, ctx, st, seeded.ID); sess != "sB" || pid != 100 {
					t.Fatalf("binding = %s/%d, want sB/100", sess, pid)
				}
				if status, _ := st.ReviewStatus(ctx, seeded.ID); status != "submitted" {
					t.Fatalf("status = %q, want submitted", status)
				}
			},
		},
		{
			name:  "orphaned open review adopted when its window is dead",
			alive: map[int]bool{100: false},
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "sA", 100, "open")
			},
			w:           Window{SessionID: "sB", ClaudePID: 200},
			wantResumed: true,
			wantSeeded:  true,
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, got store.Review) {
				if got.SessionID != "sB" || got.ClaudePID != 200 {
					t.Fatalf("returned %s/%d, want sB/200", got.SessionID, got.ClaudePID)
				}
				if sess, pid := bindingOf(t, ctx, st, seeded.ID); sess != "sB" || pid != 200 {
					t.Fatalf("binding = %s/%d, want sB/200", sess, pid)
				}
			},
		},
		{
			name: "dead window's submitted review is not adopted",
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "sA", 100, "submitted")
			},
			w: Window{SessionID: "sB", ClaudePID: 200},
		},
		{
			name:  "blank-pid review never cross-adopted by another blank-pid window",
			alive: map[int]bool{0: true},
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "sA", 0, "open")
			},
			w: Window{SessionID: "sB", ClaudePID: 0},
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, _ store.Review) {
				if sess, pid := bindingOf(t, ctx, st, seeded.ID); sess != "sA" || pid != 0 {
					t.Fatalf("binding = %s/%d, want sA/0", sess, pid)
				}
			},
		},
		{
			name: "blank session id still creates",
			w:    Window{},
			after: func(t *testing.T, ctx context.Context, st *store.Store, _, got store.Review) {
				if got.SessionID != "" || got.ClaudePID != 0 {
					t.Fatalf("created %s/%d, want blank/0", got.SessionID, got.ClaudePID)
				}
			},
		},
		{
			name: "fresh closes and detaches own review then creates",
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				r, _, err := rs.Start(ctx, Window{SessionID: "s1", ClaudePID: 100}, repo, "main", "base0", false)
				if err != nil {
					t.Fatalf("seed start: %v", err)
				}
				return r
			},
			w:     Window{SessionID: "s1", ClaudePID: 100},
			fresh: true,
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, got store.Review) {
				if status, _ := st.ReviewStatus(ctx, seeded.ID); status != "closed" {
					t.Fatalf("old status = %q, want closed", status)
				}
				if sess, pid := bindingOf(t, ctx, st, seeded.ID); sess != "" || pid != 0 {
					t.Fatalf("old binding = %s/%d, want detached", sess, pid)
				}
				if r, ok, _ := st.FindReviewBySessionRepo(ctx, "s1", repo); !ok || r.ID != got.ID {
					t.Fatal("fresh review does not own the session slot")
				}
			},
		},
		{
			name:  "fresh never adopts an orphan",
			alive: map[int]bool{100: false},
			seed: func(t *testing.T, ctx context.Context, rs Resolver) store.Review {
				return seedReview(t, ctx, rs.Store, "sA", 100, "open")
			},
			w:     Window{SessionID: "sB", ClaudePID: 200},
			fresh: true,
			after: func(t *testing.T, ctx context.Context, st *store.Store, seeded, _ store.Review) {
				if sess, pid := bindingOf(t, ctx, st, seeded.ID); sess != "sA" || pid != 100 {
					t.Fatalf("orphan binding disturbed: %s/%d", sess, pid)
				}
				if status, _ := st.ReviewStatus(ctx, seeded.ID); status != "open" {
					t.Fatalf("orphan status = %q, want open", status)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rs := newResolver(t, tc.alive)
			var seeded store.Review
			if tc.seed != nil {
				seeded = tc.seed(t, ctx, rs)
			}

			got, resumed, err := rs.Start(ctx, tc.w, repo, "main", "base0", tc.fresh)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if resumed != tc.wantResumed {
				t.Fatalf("resumed = %v, want %v", resumed, tc.wantResumed)
			}
			if same := got.ID == seeded.ID; same != tc.wantSeeded {
				t.Fatalf("got id %q (seeded %q), want seeded=%v", got.ID, seeded.ID, tc.wantSeeded)
			}
			if tc.after != nil {
				tc.after(t, ctx, rs.Store, seeded, got)
			}
		})
	}
}

func TestFind(t *testing.T) {
	cases := []struct {
		name       string
		seedSess   string
		seedPID    int
		seedStatus string
		w          Window
		wantOK     bool
	}{
		{"exact session binding", "s1", 100, "open", Window{SessionID: "s1", ClaudePID: 100}, true},
		{"rotated session id falls through to pid", "sA", 100, "open", Window{SessionID: "sB", ClaudePID: 100}, true},
		{"submitted review found by pid after rotation", "sA", 100, "submitted", Window{SessionID: "sB", ClaudePID: 100}, true},
		{"no binding for a different window", "sA", 100, "open", Window{SessionID: "sB", ClaudePID: 200}, false},
		{"pid 0 never matches by window", "sA", 0, "open", Window{SessionID: "sB", ClaudePID: 0}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rs := newResolver(t, nil)
			seeded := seedReview(t, ctx, rs.Store, tc.seedSess, tc.seedPID, tc.seedStatus)

			got, ok, err := rs.Find(ctx, tc.w, repo)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.ID != seeded.ID {
				t.Fatalf("found id %q, want %q", got.ID, seeded.ID)
			}
			if sess, pid := bindingOf(t, ctx, rs.Store, seeded.ID); sess != tc.seedSess || pid != tc.seedPID {
				t.Fatalf("find wrote: binding now %s/%d", sess, pid)
			}
		})
	}
}

func TestPeek(t *testing.T) {
	cases := []struct {
		name       string
		alive      map[int]bool
		seedSess   string
		seedPID    int
		seedStatus string
		w          Window
		wantOK     bool
	}{
		{"exact session binding", nil, "s1", 100, "open", Window{SessionID: "s1", ClaudePID: 100}, true},
		{"rotated session id falls through to pid", nil, "sA", 100, "open", Window{SessionID: "sB", ClaudePID: 100}, true},
		{"dead window's open review is the adoption candidate", map[int]bool{100: false}, "sA", 100, "open", Window{SessionID: "sB", ClaudePID: 200}, true},
		{"live foreign window's review is not peeked", map[int]bool{100: true}, "sA", 100, "open", Window{SessionID: "sB", ClaudePID: 200}, false},
		{"dead window's submitted review is not adopted", nil, "sA", 100, "submitted", Window{SessionID: "sB", ClaudePID: 200}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rs := newResolver(t, tc.alive)
			seeded := seedReview(t, ctx, rs.Store, tc.seedSess, tc.seedPID, tc.seedStatus)

			got, ok, err := rs.Peek(ctx, tc.w, repo)
			if err != nil {
				t.Fatalf("peek: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.ID != seeded.ID {
				t.Fatalf("peeked id %q, want %q", got.ID, seeded.ID)
			}
			if sess, pid := bindingOf(t, ctx, rs.Store, seeded.ID); sess != tc.seedSess || pid != tc.seedPID {
				t.Fatalf("peek wrote: binding now %s/%d", sess, pid)
			}
		})
	}
}

func TestRebind(t *testing.T) {
	cases := []struct {
		name       string
		alive      map[int]bool
		seedSess   string
		seedPID    int
		seedStatus string
		w          Window
		wantSess   string
		wantPID    int
	}{
		{
			name:     "already bound is a no-op",
			seedSess: "s1", seedPID: 100, seedStatus: "open",
			w:        Window{SessionID: "s1", ClaudePID: 100},
			wantSess: "s1", wantPID: 100,
		},
		{
			name:     "rotation moves binding to the new session id",
			seedSess: "sA", seedPID: 100, seedStatus: "open",
			w:        Window{SessionID: "sB", ClaudePID: 100},
			wantSess: "sB", wantPID: 100,
		},
		{
			name:     "pid-latest review not open is skipped",
			seedSess: "sA", seedPID: 100, seedStatus: "submitted",
			w:        Window{SessionID: "sB", ClaudePID: 100},
			wantSess: "sA", wantPID: 100,
		},
		{
			name:     "dead window's open review adopted",
			alive:    map[int]bool{100: false},
			seedSess: "sA", seedPID: 100, seedStatus: "open",
			w:        Window{SessionID: "sB", ClaudePID: 200},
			wantSess: "sB", wantPID: 200,
		},
		{
			name:     "live foreign window never stolen",
			alive:    map[int]bool{100: true},
			seedSess: "sA", seedPID: 100, seedStatus: "open",
			w:        Window{SessionID: "sB", ClaudePID: 200},
			wantSess: "sA", wantPID: 100,
		},
		{
			name:     "empty session id is a no-op",
			alive:    map[int]bool{100: false},
			seedSess: "sA", seedPID: 100, seedStatus: "open",
			w:        Window{SessionID: "", ClaudePID: 200},
			wantSess: "sA", wantPID: 100,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rs := newResolver(t, tc.alive)
			seeded := seedReview(t, ctx, rs.Store, tc.seedSess, tc.seedPID, tc.seedStatus)

			if err := rs.Rebind(ctx, tc.w, repo); err != nil {
				t.Fatalf("rebind: %v", err)
			}
			if sess, pid := bindingOf(t, ctx, rs.Store, seeded.ID); sess != tc.wantSess || pid != tc.wantPID {
				t.Fatalf("binding = %s/%d, want %s/%d", sess, pid, tc.wantSess, tc.wantPID)
			}
		})
	}
}

func TestAdoptRace(t *testing.T) {
	steal := func(t *testing.T, st *store.Store) func(ctx context.Context, r store.Review) bool {
		return func(ctx context.Context, r store.Review) bool {
			if ok, err := st.RebindReview(ctx, r.ID, r.ClaudePID, "winner", 200); err != nil || !ok {
				t.Fatalf("competing rebind: ok=%v err=%v", ok, err)
			}
			return false
		}
	}

	t.Run("start loser falls through and creates its own", func(t *testing.T) {
		ctx := context.Background()
		rs := newResolver(t, nil)
		orphan := seedReview(t, ctx, rs.Store, "sA", 100, "open")
		rs.Held = steal(t, rs.Store)

		got, resumed, err := rs.Start(ctx, Window{SessionID: "loser", ClaudePID: 300}, repo, "main", "base0", false)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if resumed || got.ID == orphan.ID {
			t.Fatalf("loser must create its own: resumed=%v id=%q orphan=%q", resumed, got.ID, orphan.ID)
		}
		if got.SessionID != "loser" || got.ClaudePID != 300 {
			t.Fatalf("created %s/%d, want loser/300", got.SessionID, got.ClaudePID)
		}
		if sess, pid := bindingOf(t, ctx, rs.Store, orphan.ID); sess != "winner" || pid != 200 {
			t.Fatalf("orphan binding = %s/%d, want winner/200", sess, pid)
		}
	})

	t.Run("rebind loser returns nil and binds nothing", func(t *testing.T) {
		ctx := context.Background()
		rs := newResolver(t, nil)
		orphan := seedReview(t, ctx, rs.Store, "sA", 100, "open")
		rs.Held = steal(t, rs.Store)

		if err := rs.Rebind(ctx, Window{SessionID: "loser", ClaudePID: 300}, repo); err != nil {
			t.Fatalf("rebind after lost race: %v", err)
		}
		if sess, pid := bindingOf(t, ctx, rs.Store, orphan.ID); sess != "winner" || pid != 200 {
			t.Fatalf("orphan binding = %s/%d, want winner/200", sess, pid)
		}
		if _, ok, _ := rs.Store.FindReviewBySessionRepo(ctx, "loser", repo); ok {
			t.Fatal("loser must not hold a binding")
		}
	})
}
