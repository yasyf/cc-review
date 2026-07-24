package store

import (
	"context"
	"errors"
	"testing"
)

func TestAIRequestAskAnswerRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	rid := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	ar, err := s.CreateAIRequest(ctx, rid, 1, "user", "mark the boring ones")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionAIRequest(ctx, ar.ID, "working", "", nil); err != nil {
		t.Fatal(err)
	}

	q := AIQuestion{Body: "Which count as boring?", Ask: &Ask{
		Header: "Scope", Options: []AskOption{{Label: "Generated only"}, {Label: "All non-test"}},
	}}
	parked, err := s.AskAIRequest(ctx, ar.ID, q)
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if parked.Status != "awaiting_input" {
		t.Fatalf("status=%s want awaiting_input", parked.Status)
	}
	if parked.Question == nil || parked.Question.Body != q.Body || parked.Question.Ask == nil || len(parked.Question.Ask.Options) != 2 {
		t.Fatalf("question not persisted: %+v", parked.Question)
	}

	// awaiting_input is parked: excluded from the redelivery set, listed separately.
	open, err := s.ListOpenAIRequests(ctx, rid, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("awaiting_input leaked into the open set: %d", len(open))
	}
	parkedList, err := s.AwaitingInputRequests(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	if len(parkedList) != 1 || parkedList[0].ID != ar.ID {
		t.Fatalf("awaiting list = %+v", parkedList)
	}

	answered, err := s.AnswerAIRequest(ctx, ar.ID, AIAnswer{AskAnswer: &AskAnswer{Selected: []string{"Generated only"}}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if answered.Status != "answered" {
		t.Fatalf("status=%s want answered", answered.Status)
	}
	if answered.Attempt != 1 {
		t.Fatalf("attempt=%d want 1", answered.Attempt)
	}
	if answered.Answer == nil || answered.Answer.AskAnswer == nil || len(answered.Answer.AskAnswer.Selected) != 1 {
		t.Fatalf("answer not persisted: %+v", answered.Answer)
	}

	// answered re-enters the redelivery set so a fresh dispatch resumes it.
	open, err = s.ListOpenAIRequests(ctx, rid, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != ar.ID {
		t.Fatalf("answered not re-offered: %+v", open)
	}

	if _, err := s.TransitionAIRequest(ctx, ar.ID, "working", "", nil); err != nil {
		t.Fatalf("resume answered→working: %v", err)
	}
	if _, err := s.TransitionAIRequest(ctx, ar.ID, "done", "did it", nil); err != nil {
		t.Fatalf("working→done: %v", err)
	}
	if _, err := s.AnswerAIRequest(ctx, ar.ID, AIAnswer{Text: "x"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("answer on done: want ErrInvalidTransition, got %v", err)
	}
}

func TestOrganizationValidatePartial(t *testing.T) {
	paths := []string{"a.go", "b.go"}
	covered := func(p ...ChapterFile) Organization {
		return Organization{Chapters: []Chapter{{Title: "t", Summary: "s", Files: p}}}
	}

	// Strict Validate requires full coverage; partial allows files not yet placed.
	one := covered(ChapterFile{Path: "a.go", Risk: "low", Rationale: "r"})
	if err := one.Validate(paths); err == nil {
		t.Fatal("Validate: want error for missing b.go")
	}
	if err := one.ValidatePartial(paths); err != nil {
		t.Fatalf("ValidatePartial: unexpected error %v", err)
	}

	// Partial still rejects unknown paths and bad risk levels.
	unknown := covered(ChapterFile{Path: "c.go", Risk: "low", Rationale: "r"})
	if err := unknown.ValidatePartial(paths); err == nil {
		t.Fatal("ValidatePartial: want error for unknown c.go")
	}
	badRisk := covered(ChapterFile{Path: "a.go", Risk: "spicy", Rationale: "r"})
	if err := badRisk.ValidatePartial(paths); err == nil {
		t.Fatal("ValidatePartial: want error for unknown risk")
	}
}

func TestAnnotationsCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	rid := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	v, sec := seedFlatVersion(ctx, t, s, rid, "main", "HEAD", "", `[{"path":"a.go","status":"M","generated":false,"vendored":false}]`)

	id1, err := s.CreateAnnotation(ctx, Annotation{
		VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", StartLine: 3, EndLine: 7, Label: "real change", AIRequestID: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAnnotation(ctx, Annotation{
		VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "deletions", StartLine: 1, EndLine: 1, AIRequestID: 0,
	}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListAnnotationsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list=%d want 2", len(list))
	}
	if list[0].ID != id1 || list[0].StartLine != 3 || list[0].EndLine != 7 || list[0].Label != "real change" || list[0].Side != "additions" {
		t.Fatalf("annotation roundtrip mismatch: %+v", list[0])
	}

	// Undo removes only the originating request's highlights.
	n, err := s.DeleteAnnotationsByAIRequest(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted=%d want 1", n)
	}
	list, err = s.ListAnnotationsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AIRequestID != 0 {
		t.Fatalf("after delete: %+v", list)
	}
}
