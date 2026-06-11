package store

import (
	"context"
	"reflect"
	"testing"
)

func TestAttributionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")

	empty, err := s.ListAttributionsByVersion(ctx, v.ID)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty list: %+v err=%v, want none", empty, err)
	}

	byFile := map[string][]AttributionRange{
		"a.go": {{Start: 1, End: 4, TurnID: 7}, {Start: 10, End: 10}},
		"b.go": {{Start: 2, End: 3, TurnID: 9}},
	}
	if err := s.PutAttributions(ctx, v.ID, byFile); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.ListAttributionsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !reflect.DeepEqual(got, byFile) {
		t.Fatalf("round-trip = %+v, want %+v", got, byFile)
	}
}

func TestPutAttributionsReplacesOnConflict(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")

	if err := s.PutAttributions(ctx, v.ID, map[string][]AttributionRange{
		"a.go": {{Start: 1, End: 4, TurnID: 7}},
		"b.go": {{Start: 5, End: 6, TurnID: 8}},
	}); err != nil {
		t.Fatalf("first put: %v", err)
	}
	replacement := []AttributionRange{{Start: 2, End: 2, TurnID: 11}}
	if err := s.PutAttributions(ctx, v.ID, map[string][]AttributionRange{"a.go": replacement}); err != nil {
		t.Fatalf("second put: %v", err)
	}

	got, err := s.ListAttributionsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := map[string][]AttributionRange{
		"a.go": replacement,
		"b.go": {{Start: 5, End: 6, TurnID: 8}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after replace = %+v, want %+v", got, want)
	}
}
