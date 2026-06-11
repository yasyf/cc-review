package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func seedAIRequest(t *testing.T, s *Store, status string) (string, int64) {
	t.Helper()
	ctx := context.Background()
	r, err := s.CreateReview(ctx, "", 0, "/repo/"+status+t.Name(), "main", "base0")
	if err != nil {
		t.Fatal(err)
	}
	ar, err := s.CreateAIRequest(ctx, r.ID, 1, "user", "mark the easy ones")
	if err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		if _, err := s.db.ExecContext(ctx, `UPDATE ai_requests SET status=? WHERE id=?`, status, ar.ID); err != nil {
			t.Fatal(err)
		}
	}
	return r.ID, ar.ID
}

func TestCreateAIRequestDefaults(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")

	ar, err := s.CreateAIRequest(ctx, r.ID, 2, "system", "Organize this review into chapters and rate per-file risk.")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetAIRequest(ctx, ar.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "pending" || got.Source != "system" || got.VersionNumber != 2 {
		t.Fatalf("got %+v, want pending/system/v2", got)
	}
	if got.Unmatched == nil || len(got.Unmatched) != 0 || got.Changes == nil || len(got.Changes) != 0 {
		t.Fatalf("unmatched=%v changes=%v, want empty non-nil", got.Unmatched, got.Changes)
	}
}

func TestTransitionAIRequestGuards(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	for _, tc := range []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{"pending to working", "pending", "working", false},
		{"pending to done", "pending", "done", false},
		{"pending to failed", "pending", "failed", false},
		{"working to done", "working", "done", false},
		{"working to failed", "working", "failed", false},
		{"done to undone", "done", "undone", false},
		{"pending to undone", "pending", "undone", true},
		{"working to undone", "working", "undone", true},
		{"done to working", "done", "working", true},
		{"done to failed", "done", "failed", true},
		{"failed to done", "failed", "done", true},
		{"undone to done", "undone", "done", true},
		{"working to working", "working", "working", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, id := seedAIRequest(t, s, tc.from)
			updated, err := s.TransitionAIRequest(ctx, id, tc.to, "", nil)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("err = %v, want ErrInvalidTransition", err)
				}
				got, _ := s.GetAIRequest(ctx, id)
				if got.Status != tc.from {
					t.Fatalf("status moved to %q on a refused transition", got.Status)
				}
				return
			}
			if err != nil {
				t.Fatalf("transition: %v", err)
			}
			if updated.Status != tc.to {
				t.Fatalf("status = %q, want %q", updated.Status, tc.to)
			}
		})
	}

	t.Run("unknown target status", func(t *testing.T) {
		_, id := seedAIRequest(t, s, "pending")
		if _, err := s.TransitionAIRequest(ctx, id, "pending", "", nil); err == nil {
			t.Fatal("want error for an unknown target status")
		}
	})

	t.Run("missing id", func(t *testing.T) {
		if _, err := s.TransitionAIRequest(ctx, 99999, "working", "", nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestTransitionAIRequestSummaryAndUnmatched(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, id := seedAIRequest(t, s, "working")

	unmatched := []Unmatched{{Pattern: "old tests", Why: "no test file predates v1"}}
	updated, err := s.TransitionAIRequest(ctx, id, "done", "marked 3 mechanical files", unmatched)
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if updated.Summary != "marked 3 mechanical files" || !reflect.DeepEqual(updated.Unmatched, unmatched) {
		t.Fatalf("got summary=%q unmatched=%+v", updated.Summary, updated.Unmatched)
	}

	// Undo keeps the stored summary and unmatched (empty/nil = no change).
	undone, err := s.TransitionAIRequest(ctx, id, "undone", "", nil)
	if err != nil {
		t.Fatalf("undo transition: %v", err)
	}
	if undone.Summary != "marked 3 mechanical files" || !reflect.DeepEqual(undone.Unmatched, unmatched) {
		t.Fatalf("undo wiped summary/unmatched: %+v", undone)
	}
}

func TestAppendAIRequestChangesKeepsFirstPrior(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, id := seedAIRequest(t, s, "working")

	first := []AIChange{
		{Path: "a.go", Reason: "import-only", Prior: PriorState{Reviewed: false, Hidden: false, Fingerprint: ""},
			Applied: AppliedState{Reviewed: true}},
		{Path: "b.go", Reason: "lockfile", Prior: PriorState{Reviewed: true, Fingerprint: "fp-b"},
			Applied: AppliedState{Reviewed: true, Hidden: true}},
	}
	if err := s.AppendAIRequestChanges(ctx, id, first); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	second := []AIChange{
		{Path: "a.go", Reason: "also hide it", Prior: PriorState{Reviewed: true}, Applied: AppliedState{Reviewed: true, Hidden: true}},
		{Path: "c.go", Reason: "generated", Prior: PriorState{}, Applied: AppliedState{Hidden: true}},
	}
	if err := s.AppendAIRequestChanges(ctx, id, second); err != nil {
		t.Fatalf("second batch: %v", err)
	}

	got, err := s.GetAIRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	want := []AIChange{
		// a.go: FIRST prior kept, latest applied + reason.
		{Path: "a.go", Reason: "also hide it", Prior: PriorState{}, Applied: AppliedState{Reviewed: true, Hidden: true}},
		{Path: "b.go", Reason: "lockfile", Prior: PriorState{Reviewed: true, Fingerprint: "fp-b"},
			Applied: AppliedState{Reviewed: true, Hidden: true}},
		{Path: "c.go", Reason: "generated", Prior: PriorState{}, Applied: AppliedState{Hidden: true}},
	}
	if !reflect.DeepEqual(got.Changes, want) {
		t.Fatalf("changes = %+v\nwant %+v", got.Changes, want)
	}
}

func TestListAIRequestsNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")

	older, _ := s.CreateAIRequest(ctx, r.ID, 1, "system", "organize")
	newer, _ := s.CreateAIRequest(ctx, r.ID, 1, "user", "mark renames")

	got, err := s.ListAIRequests(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("order = %+v, want newest first", got)
	}
}
