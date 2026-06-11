package store

import (
	"context"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func stateByPath(t *testing.T, s *Store, reviewID string) map[string]FileState {
	t.Helper()
	states, err := s.ListFileStates(context.Background(), reviewID)
	if err != nil {
		t.Fatalf("list file states: %v", err)
	}
	out := map[string]FileState{}
	for _, st := range states {
		out[st.Path] = st
	}
	return out
}

func TestApplyFileStatesPartialFlags(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	fps := map[string]string{"a.go": "fp-a", "b.go": "fp-b"}

	for _, tc := range []struct {
		name         string
		inputs       []FileStateInput
		wantReviewed bool
		wantHidden   bool
		wantFP       string
	}{
		{"reviewed only creates the row", []FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}}, true, false, "fp-a"},
		{"hidden only preserves reviewed and its stamp", []FileStateInput{{Path: "a.go", Hidden: boolPtr(true)}}, true, true, "fp-a"},
		{"unreview clears the stamp, hidden survives", []FileStateInput{{Path: "a.go", Reviewed: boolPtr(false)}}, false, true, ""},
		{"both flags at once", []FileStateInput{{Path: "a.go", Reviewed: boolPtr(true), Hidden: boolPtr(false)}}, true, false, "fp-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := s.ApplyFileStates(ctx, r.ID, tc.inputs, fps)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("got %d results, want 1", len(results))
			}
			if results[0].Applied.Reviewed != tc.wantReviewed || results[0].Applied.Hidden != tc.wantHidden {
				t.Fatalf("applied = %+v, want reviewed=%v hidden=%v", results[0].Applied, tc.wantReviewed, tc.wantHidden)
			}
			st := stateByPath(t, s, r.ID)["a.go"]
			if st.Reviewed != tc.wantReviewed || st.Hidden != tc.wantHidden || st.ReviewedFingerprint != tc.wantFP {
				t.Fatalf("stored = %+v, want reviewed=%v hidden=%v fp=%q", st, tc.wantReviewed, tc.wantHidden, tc.wantFP)
			}
		})
	}
}

func TestApplyFileStatesReturnsPrior(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	fps := map[string]string{"a.go": "fp-a"}

	if _, err := s.ApplyFileStates(ctx, r.ID, []FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}}, fps); err != nil {
		t.Fatal(err)
	}
	results, err := s.ApplyFileStates(ctx, r.ID, []FileStateInput{{Path: "a.go", Reviewed: boolPtr(false), Hidden: boolPtr(true)}}, fps)
	if err != nil {
		t.Fatal(err)
	}
	want := PriorState{Reviewed: true, Hidden: false, Fingerprint: "fp-a"}
	if results[0].Prior != want {
		t.Fatalf("prior = %+v, want %+v", results[0].Prior, want)
	}
}

func TestApplyFileStatesKeepsStampWhenAlreadyReviewed(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")

	if _, err := s.ApplyFileStates(ctx, r.ID, []FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}},
		map[string]string{"a.go": "fp-v1"}); err != nil {
		t.Fatal(err)
	}
	// A later batch against a newer fingerprint map must not restamp: reviewed
	// survives exactly while the mark-time fingerprint matches.
	if _, err := s.ApplyFileStates(ctx, r.ID, []FileStateInput{{Path: "a.go", Hidden: boolPtr(true)}},
		map[string]string{"a.go": "fp-v2"}); err != nil {
		t.Fatal(err)
	}
	if st := stateByPath(t, s, r.ID)["a.go"]; st.ReviewedFingerprint != "fp-v1" {
		t.Fatalf("stamp = %q, want the mark-time fp-v1", st.ReviewedFingerprint)
	}
}

func TestUnreviewChangedFiles(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	v1 := map[string]string{"changed.go": "old", "kept.go": "same", "gone.go": "was", "hiddenchanged.go": "old"}

	if _, err := s.ApplyFileStates(ctx, r.ID, []FileStateInput{
		{Path: "changed.go", Reviewed: boolPtr(true)},
		{Path: "kept.go", Reviewed: boolPtr(true)},
		{Path: "gone.go", Reviewed: boolPtr(true)},
		{Path: "hiddenchanged.go", Reviewed: boolPtr(true), Hidden: boolPtr(true)},
	}, v1); err != nil {
		t.Fatal(err)
	}

	// v2: changed.go and hiddenchanged.go changed, kept.go unchanged, gone.go disappeared.
	v2 := map[string]string{"changed.go": "new", "kept.go": "same", "hiddenchanged.go": "new"}
	unmarked, err := s.UnreviewChangedFiles(ctx, r.ID, v2)
	if err != nil {
		t.Fatalf("unreview: %v", err)
	}
	if len(unmarked) != 2 || unmarked[0].Path != "changed.go" || unmarked[1].Path != "hiddenchanged.go" {
		t.Fatalf("unmarked = %+v, want changed.go + hiddenchanged.go", unmarked)
	}
	if !unmarked[1].Hidden {
		t.Fatal("unmark must preserve the hidden flag in its result")
	}

	got := stateByPath(t, s, r.ID)
	if got["changed.go"].Reviewed || got["changed.go"].ReviewedFingerprint != "" {
		t.Fatalf("changed.go = %+v, want unmarked with cleared stamp", got["changed.go"])
	}
	if !got["kept.go"].Reviewed || got["kept.go"].ReviewedFingerprint != "same" {
		t.Fatalf("kept.go = %+v, want still reviewed", got["kept.go"])
	}
	if !got["gone.go"].Reviewed {
		t.Fatalf("gone.go = %+v, want untouched (disappeared from the diff)", got["gone.go"])
	}
	if got["hiddenchanged.go"].Reviewed || !got["hiddenchanged.go"].Hidden {
		t.Fatalf("hiddenchanged.go = %+v, want unmarked but still hidden", got["hiddenchanged.go"])
	}

	// Idempotent: a second pass with the same map unmarks nothing.
	again, err := s.UnreviewChangedFiles(ctx, r.ID, v2)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second pass unmarked %+v, want none", again)
	}
}

func TestRestoreFileStates(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	fps := map[string]string{"a.go": "fp-a", "b.go": "fp-b"}

	if _, err := s.ApplyFileStates(ctx, r.ID, []FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}}, fps); err != nil {
		t.Fatal(err)
	}
	results, err := s.ApplyFileStates(ctx, r.ID, []FileStateInput{
		{Path: "a.go", Reviewed: boolPtr(false), Hidden: boolPtr(true)},
		{Path: "b.go", Hidden: boolPtr(true)},
	}, fps)
	if err != nil {
		t.Fatal(err)
	}
	changes := make([]AIChange, 0, len(results))
	for _, res := range results {
		changes = append(changes, AIChange{Path: res.Path, Prior: res.Prior, Applied: res.Applied})
	}

	if err := s.RestoreFileStates(ctx, r.ID, changes); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := stateByPath(t, s, r.ID)
	if !got["a.go"].Reviewed || got["a.go"].Hidden || got["a.go"].ReviewedFingerprint != "fp-a" {
		t.Fatalf("a.go = %+v, want pre-batch reviewed state restored", got["a.go"])
	}
	if got["b.go"].Reviewed || got["b.go"].Hidden {
		t.Fatalf("b.go = %+v, want pre-batch default state restored", got["b.go"])
	}
}
