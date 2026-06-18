package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func chapter(title string, paths ...string) Chapter {
	files := make([]ChapterFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, ChapterFile{Path: p, Risk: "low", Rationale: "r"})
	}
	return Chapter{Title: title, Summary: "s", Files: files}
}

// chapterWithLines covers path (carrying focus + line notes) plus rest, so a
// Validate case can exercise a line note while still satisfying 1:1 coverage.
func chapterWithLines(title, path string, lines []LineNote, rest ...string) Chapter {
	files := []ChapterFile{{Path: path, Risk: "low", Rationale: "r", Focus: "f", Lines: lines}}
	for _, p := range rest {
		files = append(files, ChapterFile{Path: p, Risk: "low", Rationale: "r"})
	}
	return Chapter{Title: title, Summary: "s", Files: files}
}

func TestOrganizationValidate(t *testing.T) {
	versionPaths := []string{"a.go", "b.go", "c.go"}
	for _, tc := range []struct {
		name    string
		org     Organization
		wantErr []string
	}{
		{"exact coverage passes", Organization{Chapters: []Chapter{chapter("one", "a.go", "b.go"), chapter("two", "c.go")}}, nil},
		{"missing path enumerated", Organization{Chapters: []Chapter{chapter("one", "a.go")}},
			[]string{"missing paths: b.go, c.go"}},
		{"unknown path enumerated", Organization{Chapters: []Chapter{chapter("one", "a.go", "b.go", "c.go", "nope.go")}},
			[]string{"unknown paths: nope.go"}},
		{"duplicate across chapters enumerated", Organization{Chapters: []Chapter{chapter("one", "a.go", "b.go"), chapter("two", "b.go", "c.go")}},
			[]string{"more than one chapter: b.go"}},
		{"missing and unknown together", Organization{Chapters: []Chapter{chapter("one", "a.go", "zzz.go")}},
			[]string{"missing paths: b.go, c.go", "unknown paths: zzz.go"}},
		{"unknown risk", Organization{Chapters: []Chapter{{Title: "one", Files: []ChapterFile{{Path: "a.go", Risk: "scary"}}}}},
			[]string{`unknown risk "scary"`}},
		{"focus and lines pass", Organization{Chapters: []Chapter{chapterWithLines("one", "a.go",
			[]LineNote{{Start: 1, End: 3, Level: "focus", Note: "n"}, {Start: 5, End: 5, Level: "mechanical"}}, "b.go", "c.go")}}, nil},
		{"unknown line level", Organization{Chapters: []Chapter{chapterWithLines("one", "a.go",
			[]LineNote{{Start: 1, End: 2, Level: "scary"}}, "b.go", "c.go")}},
			[]string{`unknown level "scary"`}},
		{"line range starts below one", Organization{Chapters: []Chapter{chapterWithLines("one", "a.go",
			[]LineNote{{Start: 0, End: 3, Level: "focus"}}, "b.go", "c.go")}},
			[]string{"invalid line range 0-3"}},
		{"line range start past end", Organization{Chapters: []Chapter{chapterWithLines("one", "a.go",
			[]LineNote{{Start: 5, End: 2, Level: "focus"}}, "b.go", "c.go")}},
			[]string{"invalid line range 5-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.org.Validate(versionPaths)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error, got nil")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q missing %q", err, want)
				}
			}
		})
	}
}

func TestLatestOrganization(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	rid := seedReview(t, s, "s", 0, "/repo", "main", "base0")
	v1, _ := s.CreateVersion(ctx, rid, "main", "HEAD", "/p1", `[{"path":"a.go","status":"M"}]`, "")

	if _, _, ok, err := s.LatestOrganization(ctx, rid); err != nil || ok {
		t.Fatalf("unorganized review: ok=%v err=%v, want absent", ok, err)
	}

	v1Org := Organization{Chapters: []Chapter{chapter("First", "a.go")}}
	if err := s.UpsertOrganization(ctx, v1.ID, v1Org); err != nil {
		t.Fatalf("upsert v1: %v", err)
	}
	// An org-less newer version is skipped: v1's organization stays the latest.
	if _, err := s.CreateVersion(ctx, rid, "main", "HEAD", "/p2", `[{"path":"a.go","status":"M"},{"path":"b.go","status":"A"}]`, ""); err != nil {
		t.Fatalf("create v2: %v", err)
	}
	org, owner, ok, err := s.LatestOrganization(ctx, rid)
	if err != nil || !ok {
		t.Fatalf("after v2: ok=%v err=%v", ok, err)
	}
	if owner.ID != v1.ID || owner.VersionNumber != 1 || owner.FilesJSON != v1.FilesJSON {
		t.Fatalf("owner = %+v, want v1", owner)
	}
	if !reflect.DeepEqual(org, v1Org) {
		t.Fatalf("org = %+v, want %+v", org, v1Org)
	}

	v3, _ := s.CreateVersion(ctx, rid, "main", "HEAD", "/p3", `[{"path":"a.go","status":"M"}]`, "")
	v3Org := Organization{Chapters: []Chapter{chapter("Third", "a.go")}}
	if err := s.UpsertOrganization(ctx, v3.ID, v3Org); err != nil {
		t.Fatalf("upsert v3: %v", err)
	}
	org, owner, ok, err = s.LatestOrganization(ctx, rid)
	if err != nil || !ok {
		t.Fatalf("after v3 org: ok=%v err=%v", ok, err)
	}
	if owner.ID != v3.ID || owner.VersionNumber != 3 {
		t.Fatalf("owner = %+v, want v3", owner)
	}
	if !reflect.DeepEqual(org, v3Org) {
		t.Fatalf("org = %+v, want %+v", org, v3Org)
	}
}

func TestOrganizationUpsertRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	rid := seedReview(t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, rid, "main", "HEAD", "/p", "[]", "")

	if _, ok, err := s.GetOrganization(ctx, v.ID); err != nil || ok {
		t.Fatalf("empty get: ok=%v err=%v, want absent", ok, err)
	}

	overview := "Adds per-file review state."
	org := Organization{Overview: &overview, Chapters: []Chapter{
		{Title: "Store", Summary: "DDL first.", Files: []ChapterFile{{Path: "store.go", Risk: "high", Rationale: "new DDL"}}},
	}}
	if err := s.UpsertOrganization(ctx, v.ID, org); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := s.GetOrganization(ctx, v.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, org) {
		t.Fatalf("round-trip = %+v, want %+v", got, org)
	}

	// Upsert replaces, including dropping the overview to null.
	replacement := Organization{Chapters: []Chapter{chapter("Everything", "store.go")}}
	if err := s.UpsertOrganization(ctx, v.ID, replacement); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _, _ = s.GetOrganization(ctx, v.ID)
	if got.Overview != nil || len(got.Chapters) != 1 || got.Chapters[0].Title != "Everything" {
		t.Fatalf("replacement = %+v", got)
	}
}

// A legacy blob written before focus/lines existed decodes with zero values, so
// older organizations keep loading without a migration.
func TestOrganizationDecodeLegacyBlob(t *testing.T) {
	const legacy = `{"overview":"o","chapters":[{"title":"t","summary":"s","files":[{"path":"a.go","risk":"low","rationale":"r"}]}]}`
	var org Organization
	if err := json.Unmarshal([]byte(legacy), &org); err != nil {
		t.Fatalf("decode: %v", err)
	}
	f := org.Chapters[0].Files[0]
	if f.Focus != "" {
		t.Fatalf("focus = %q, want empty", f.Focus)
	}
	if f.Lines != nil {
		t.Fatalf("lines = %v, want nil", f.Lines)
	}
}

func TestOrganizationUpsertRoundTripWithLines(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	rid := seedReview(t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, rid, "main", "HEAD", "/p", "[]", "")

	org := Organization{Chapters: []Chapter{{Title: "Store", Summary: "s", Files: []ChapterFile{
		{Path: "annotated.go", Risk: "high", Rationale: "r", Focus: "scrutinize the new guard", Lines: []LineNote{
			{Start: 1, End: 4, Level: "focus", Note: "the guard"},
			{Start: 10, End: 12, Level: "mechanical"},
		}},
		{Path: "plain.go", Risk: "low", Rationale: "r"},
	}}}}
	if err := s.UpsertOrganization(ctx, v.ID, org); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := s.GetOrganization(ctx, v.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, org) {
		t.Fatalf("round-trip = %+v, want %+v", got, org)
	}
}
