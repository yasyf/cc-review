package store

import (
	"context"
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
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	v1, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p1", `[{"path":"a.go","status":"M"}]`)

	if _, _, ok, err := s.LatestOrganization(ctx, r.ID); err != nil || ok {
		t.Fatalf("unorganized review: ok=%v err=%v, want absent", ok, err)
	}

	v1Org := Organization{Chapters: []Chapter{chapter("First", "a.go")}}
	if err := s.UpsertOrganization(ctx, v1.ID, v1Org); err != nil {
		t.Fatalf("upsert v1: %v", err)
	}
	// An org-less newer version is skipped: v1's organization stays the latest.
	if _, err := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p2", `[{"path":"a.go","status":"M"},{"path":"b.go","status":"A"}]`); err != nil {
		t.Fatalf("create v2: %v", err)
	}
	org, owner, ok, err := s.LatestOrganization(ctx, r.ID)
	if err != nil || !ok {
		t.Fatalf("after v2: ok=%v err=%v", ok, err)
	}
	if owner.ID != v1.ID || owner.VersionNumber != 1 || owner.FilesJSON != v1.FilesJSON {
		t.Fatalf("owner = %+v, want v1", owner)
	}
	if !reflect.DeepEqual(org, v1Org) {
		t.Fatalf("org = %+v, want %+v", org, v1Org)
	}

	v3, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p3", `[{"path":"a.go","status":"M"}]`)
	v3Org := Organization{Chapters: []Chapter{chapter("Third", "a.go")}}
	if err := s.UpsertOrganization(ctx, v3.ID, v3Org); err != nil {
		t.Fatalf("upsert v3: %v", err)
	}
	org, owner, ok, err = s.LatestOrganization(ctx, r.ID)
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
	r, _ := s.CreateReview(ctx, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")

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
