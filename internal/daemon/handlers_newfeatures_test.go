package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/store"
)

func TestHandleFileStatesByRisk(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(ctx, t, s, repo)

	org := req
	org.Organization = &store.Organization{Chapters: []store.Chapter{{Title: "All", Summary: "s", Files: []store.ChapterFile{
		{Path: "a.go", Risk: "low", Rationale: "r"},
		{Path: "b.go", Risk: "mechanical", Rationale: "r"},
	}}}}
	org.VersionNumber = started.Version
	if resp := s.handleSubmitOrganization(ctx, org); !resp.OK {
		t.Fatalf("submit: %s", resp.Error)
	}

	t.Run("missing risk rejected", func(t *testing.T) {
		r := req
		r.Reviewed = bptr(true)
		if resp := s.handleFileStatesByRisk(ctx, r); resp.OK || !strings.Contains(resp.Error, "at least one risk") {
			t.Fatalf("ok=%v err=%q, want missing-risk rejection", resp.OK, resp.Error)
		}
	})

	t.Run("flips only the mechanical-tagged files", func(t *testing.T) {
		r := req
		r.Risk = []string{"mechanical"}
		r.Reviewed = bptr(true)
		r.Reason = "tagged mechanical"
		resp := s.handleFileStatesByRisk(ctx, r)
		if !resp.OK {
			t.Fatalf("by-risk: %s", resp.Error)
		}
		if len(resp.Paths) != 1 || resp.Paths[0] != "b.go" {
			t.Fatalf("paths=%v, want [b.go]", resp.Paths)
		}
		states, err := s.store.ListFileStates(ctx, started.ReviewID)
		if err != nil {
			t.Fatal(err)
		}
		reviewed := map[string]bool{}
		for _, st := range states {
			reviewed[st.Path] = st.Reviewed
		}
		if !reviewed["b.go"] || reviewed["a.go"] {
			t.Fatalf("reviewed=%v, want only b.go", reviewed)
		}
	})
}

func TestHandleAnnotate(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(ctx, t, s, repo)

	r := req
	r.Annotations = []AnnotateInput{
		{Kind: "highlight", FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "genuinely new"},
		{Kind: "comment", FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "is this intended?"},
	}
	if resp := s.handleAnnotate(ctx, r); !resp.OK {
		t.Fatalf("annotate: %s", resp.Error)
	}

	v, _, err := s.store.LatestVersion(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	anns, err := s.store.ListAnnotationsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 || anns[0].Label != "genuinely new" || anns[0].Side != "additions" {
		t.Fatalf("annotations=%+v, want one highlight", anns)
	}
	comments, err := s.store.ListCommentsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Author != store.OriginClaude || comments[0].Body != "is this intended?" {
		t.Fatalf("comments=%+v, want one claude comment", comments)
	}

	t.Run("comment kind needs a body", func(t *testing.T) {
		bad := req
		bad.Annotations = []AnnotateInput{{Kind: "comment", FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1}}
		if resp := s.handleAnnotate(ctx, bad); resp.OK || !strings.Contains(resp.Error, "needs a body") {
			t.Fatalf("ok=%v err=%q, want body rejection", resp.OK, resp.Error)
		}
	})

	t.Run("unknown path rejected", func(t *testing.T) {
		bad := req
		bad.Annotations = []AnnotateInput{{Kind: "highlight", FilePath: "nope.go", Side: "additions", StartLine: 1, EndLine: 1}}
		if resp := s.handleAnnotate(ctx, bad); resp.OK || !strings.Contains(resp.Error, "unknown path") {
			t.Fatalf("ok=%v err=%q, want unknown-path rejection", resp.OK, resp.Error)
		}
	})
}
