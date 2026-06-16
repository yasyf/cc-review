package store

import (
	"context"
	"reflect"
	"testing"
)

func TestAttributionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	rid := seedReview(t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, rid, "main", "HEAD", "/p", "[]")

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

func TestListAttributionsBySession(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r1id := seedReview(t, s, "sess-a", 0, "/repo", "main", "base0")
	v1, _ := s.CreateVersion(ctx, r1id, "main", "HEAD", "/p", "[]")
	v2, _ := s.CreateVersion(ctx, r1id, "main", "HEAD", "/p", "[]")
	otherid := seedReview(t, s, "sess-b", 0, "/repo2", "main", "base0")
	vOther, _ := s.CreateVersion(ctx, otherid, "main", "HEAD", "/p", "[]")

	if err := s.PutAttributions(ctx, v1.ID, map[string][]AttributionRange{
		"b.go": {{Start: 2, End: 3, TurnID: 9}},
		"a.go": {{Start: 1, End: 4, TurnID: 7}, {Start: 10, End: 10}},
	}); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if err := s.PutAttributions(ctx, v2.ID, map[string][]AttributionRange{
		"a.go": {{Start: 5, End: 6, TurnID: 9}},
	}); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	if err := s.PutAttributions(ctx, vOther.ID, map[string][]AttributionRange{
		"c.go": {{Start: 1, End: 1, TurnID: 12}},
	}); err != nil {
		t.Fatalf("put other session: %v", err)
	}

	got, err := s.ListAttributionsBySession(ctx, "sess-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []SessionAttribution{
		{ReviewID: r1id, Version: 1, FilePath: "a.go", Ranges: []AttributionRange{{Start: 1, End: 4, TurnID: 7}, {Start: 10, End: 10}}},
		{ReviewID: r1id, Version: 1, FilePath: "b.go", Ranges: []AttributionRange{{Start: 2, End: 3, TurnID: 9}}},
		{ReviewID: r1id, Version: 2, FilePath: "a.go", Ranges: []AttributionRange{{Start: 5, End: 6, TurnID: 9}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session attributions = %+v, want %+v", got, want)
	}

	none, err := s.ListAttributionsBySession(ctx, "sess-none")
	if err != nil || len(none) != 0 {
		t.Fatalf("unknown session: %+v err=%v, want none", none, err)
	}
}

func TestPutAttributionsReplacesOnConflict(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	rid := seedReview(t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, rid, "main", "HEAD", "/p", "[]")

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
