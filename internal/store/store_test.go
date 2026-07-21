package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	ccevent "github.com/yasyf/cc-interact/event"
	ccstore "github.com/yasyf/cc-interact/store"
)

func openTestStore(t *testing.T) *Store {
	return openTestStoreContext(t.Context(), t)
}

func openTestStoreContext(ctx context.Context, t *testing.T) *Store {
	t.Helper()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSchemaV1ReopensAndRejectsForeignFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open v1: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close v1: %v", err)
	}
	if s, err = Open(t.Context(), path); err != nil {
		t.Fatalf("reopen exact v1: %v", err)
	}
	_ = s.Close()

	foreignPath := filepath.Join(t.TempDir(), "foreign.db")
	foreign, err := ccstore.Open(t.Context(), foreignPath, ccstore.Schema{DDL: `CREATE TABLE foreign_state(id TEXT PRIMARY KEY);`})
	if err != nil {
		t.Fatalf("create foreign database: %v", err)
	}
	_ = foreign.Close()
	if _, err := Open(t.Context(), foreignPath); err == nil || !strings.Contains(err.Error(), "schema fingerprint") {
		t.Fatalf("Open(foreign) = %v, want fingerprint rejection", err)
	}
}

func TestSchemaV1RejectsFingerprintMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open v1: %v", err)
	}
	_ = s.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	if _, err := db.Exec(`UPDATE cc_interact_schema_v1 SET fingerprint='foreign' WHERE id=1`); err != nil {
		t.Fatalf("corrupt marker: %v", err)
	}
	_ = db.Close()
	if _, err := Open(t.Context(), path); err == nil || !strings.Contains(err.Error(), "schema fingerprint") {
		t.Fatalf("Open(foreign marker) = %v, want fingerprint rejection", err)
	}
}

func TestVersionNumbersAreMonotonic(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")

	for want := 1; want <= 3; want++ {
		v, err := s.CreateVersion(ctx, id, "main", "HEAD", "/p.patch", "[]", "")
		if err != nil {
			t.Fatalf("create version: %v", err)
		}
		if v.VersionNumber != want {
			t.Fatalf("version number = %d, want %d", v.VersionNumber, want)
		}
	}
	latest, ok, _ := s.LatestVersion(ctx, id)
	if !ok || latest.VersionNumber != 3 {
		t.Fatalf("latest = %d (ok=%v), want 3", latest.VersionNumber, ok)
	}
}

func TestReplyDedupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, id, "main", "HEAD", "/p", "[]", "")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	id1, ins1, err := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "why?", DedupKey: "k1"})
	if err != nil || !ins1 {
		t.Fatalf("first reply: ins=%v err=%v", ins1, err)
	}
	id2, ins2, err := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "why?", DedupKey: "k1"})
	if err != nil {
		t.Fatalf("second reply: %v", err)
	}
	if ins2 {
		t.Fatal("redelivered reply should not insert again")
	}
	if id1 != id2 {
		t.Fatalf("dedup returned id %d, want existing %d", id2, id1)
	}

	replies, _ := s.ListRepliesByComment(ctx, cid)
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1 (deduped)", len(replies))
	}
}

func TestConcurrentReplyDedupNeverErrors(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, id, "main", "HEAD", "/p", "[]", "")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	// Fire many redeliveries of the same reply concurrently. Before the ON
	// CONFLICT fix the losing writers hit a UNIQUE-constraint error; now every
	// call must succeed, exactly one inserts, and all return the same id.
	const n = 16
	ids := make([]int64, n)
	inserted := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], inserted[i], errs[i] = s.CreateReply(ctx, Reply{
				CommentID: cid, Origin: "claude", Kind: "question", Body: "why?", DedupKey: "k",
			})
		}(i)
	}
	wg.Wait()

	insertCount := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("call %d errored: %v", i, errs[i])
		}
		if inserted[i] {
			insertCount++
		}
		if ids[i] != ids[0] {
			t.Fatalf("call %d returned id %d, want %d (all should share the deduped id)", i, ids[i], ids[0])
		}
	}
	if insertCount != 1 {
		t.Fatalf("inserted=%d, want exactly 1", insertCount)
	}
	replies, _ := s.ListRepliesByComment(ctx, cid)
	if len(replies) != 1 {
		t.Fatalf("got %d rows, want 1", len(replies))
	}
}

func TestMaxEventSeq(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")

	for i := 1; i <= 3; i++ {
		if _, err := s.cc.AppendEvent(ctx, &ccevent.Event{
			SubjectID: id, Origin: ccevent.OriginHuman, Type: "t", Payload: []byte(`{"x":1}`),
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if seq, err := s.MaxEventSeq(ctx, id); err != nil || seq != 3 {
		t.Fatalf("max event seq = %d err=%v, want 3", seq, err)
	}

	empty := seedReview(ctx, t, s, "s-empty", 0, "/repo/empty", "main", "base0")
	if seq, err := s.MaxEventSeq(ctx, empty); err != nil || seq != 0 {
		t.Fatalf("max event seq of eventless review = %d err=%v, want 0", seq, err)
	}
}

func TestStaleConnectedReviews(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	channel := func(reviewID string, connected bool) {
		t.Helper()
		payload := `{"type":"channel.changed","connected":false}`
		if connected {
			payload = `{"type":"channel.changed","connected":true}`
		}
		if _, err := s.cc.AppendEvent(ctx, &ccevent.Event{
			SubjectID: reviewID, Origin: ccevent.OriginSystem, Type: EventChannelChanged, Payload: []byte(payload),
		}); err != nil {
			t.Fatal(err)
		}
	}

	staleTrue := seedReview(ctx, t, s, "s1", 0, "/repo/a", "main", "base0")
	channel(staleTrue, true)

	closedFalse := seedReview(ctx, t, s, "s2", 0, "/repo/b", "main", "base0")
	channel(closedFalse, true)
	channel(closedFalse, false)

	reopened := seedReview(ctx, t, s, "s3", 0, "/repo/c", "main", "base0")
	channel(reopened, false)
	channel(reopened, true)

	// connected:true on another event type never counts.
	otherType := seedReview(ctx, t, s, "s4", 0, "/repo/d", "main", "base0")
	if _, err := s.cc.AppendEvent(ctx, &ccevent.Event{
		SubjectID: otherType, Origin: ccevent.OriginSystem, Type: "t", Payload: []byte(`{"connected":true}`),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.StaleConnectedReviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{staleTrue: true, reopened: true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] || got[0] == got[1] {
		t.Fatalf("stale reviews = %v, want exactly {%s, %s}", got, staleTrue, reopened)
	}
}

func TestReviewSlug(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name   string
		branch string
		want   string
	}{
		{"nested branch", "feat/a/b", "feat--a--b--01234567"},
		{"empty branch (detached HEAD)", "", "01234567"},
		{"branch already containing --", "feat--x", "feat--x--01234567"},
		{"dotted release branch", "release-1.2", "release-1.2--01234567"},
		{"hash mark sanitized", "wip#2", "wip-2--01234567"},
		{"space sanitized", "a b", "a-b--01234567"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReviewSlug(tc.branch, id); got != tc.want {
				t.Fatalf("ReviewSlug(%q) = %q, want %q", tc.branch, got, tc.want)
			}
		})
	}
}

func TestGetReviewByRef(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	ss := newSubjectStoreForTest(s)
	id := NewSlugHash()
	slug := ReviewSlug("feat/login", id)
	sub, err := ss.Create(ctx, id, slug, "s", "/repo", 0, "open")
	if err != nil {
		t.Fatal(err)
	}
	if want := "feat--login--" + id[:8]; slug != want {
		t.Fatalf("slug = %q, want %q", slug, want)
	}

	bySlug, err := s.GetReviewByRef(ctx, slug)
	if err != nil || bySlug.ID != sub.ID {
		t.Fatalf("by slug: id=%q err=%v, want %q", bySlug.ID, err, sub.ID)
	}
	byID, err := s.GetReviewByRef(ctx, sub.ID)
	if err != nil || byID.ID != sub.ID {
		t.Fatalf("by id: id=%q err=%v, want %q", byID.ID, err, sub.ID)
	}
	if _, err := s.GetReviewByRef(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ref err = %v, want ErrNotFound", err)
	}
}

func TestAskReplyRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, id, "main", "HEAD", "/p", "[]", "")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	ask := &Ask{Header: "Approach", MultiSelect: true, Options: []AskOption{
		{Label: "Keep as-is", Description: "minimal churn"},
		{Label: "Extract a helper", Preview: "func helper() {}"},
	}}
	rid, inserted, err := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "Which?", Ask: ask})
	if err != nil || !inserted {
		t.Fatalf("create ask: ins=%v err=%v", inserted, err)
	}

	got, err := s.GetReply(ctx, rid)
	if err != nil {
		t.Fatalf("get reply: %v", err)
	}
	if got.Kind != "ask" || got.Ask == nil {
		t.Fatalf("got kind=%q ask=%v, want decoded ask", got.Kind, got.Ask)
	}
	if !reflect.DeepEqual(*got.Ask, *ask) {
		t.Fatalf("ask round-trip = %+v, want %+v", *got.Ask, *ask)
	}
	if got.AskAnswer != nil {
		t.Fatalf("unanswered ask has AskAnswer %+v", *got.AskAnswer)
	}

	for _, tc := range []struct {
		name string
		r    Reply
	}{
		{"ask without payload", Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "x"}},
		{"non-ask with payload", Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "x", Ask: ask}},
		{"empty options", Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "x", Ask: &Ask{}}},
		{"duplicate labels", Reply{
			CommentID: cid, Origin: "claude", Kind: "ask", Body: "x",
			Ask: &Ask{Options: []AskOption{{Label: "A"}, {Label: "A"}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.CreateReply(ctx, tc.r); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestAnswerAsk(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, id, "main", "HEAD", "/p", "[]", "")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	newAsk := func(multi bool) int64 {
		id, _, err := s.CreateReply(ctx, Reply{
			CommentID: cid, Origin: "claude", Kind: "ask", Body: "Q",
			Ask: &Ask{MultiSelect: multi, Options: []AskOption{{Label: "A"}, {Label: "B"}}},
		})
		if err != nil {
			t.Fatalf("seed ask: %v", err)
		}
		return id
	}

	for _, tc := range []struct {
		name    string
		multi   bool
		ans     AskAnswer
		wantErr bool
	}{
		{"single select", false, AskAnswer{Selected: []string{"A"}}, false},
		{"multi select two", true, AskAnswer{Selected: []string{"A", "B"}}, false},
		{"other only", false, AskAnswer{Other: "something else"}, false},
		{"notes ride along", false, AskAnswer{Selected: []string{"B"}, Notes: "context"}, false},
		{"label not offered", false, AskAnswer{Selected: []string{"C"}}, true},
		{"two picks single-select", false, AskAnswer{Selected: []string{"A", "B"}}, true},
		{"selected plus other single-select", false, AskAnswer{Selected: []string{"A"}, Other: "x"}, true},
		{"empty answer", false, AskAnswer{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			askID := newAsk(tc.multi)
			err := s.AnswerAsk(ctx, askID, tc.ans, "web")
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("answer ask: %v", err)
			}
			got, err := s.GetReply(ctx, askID)
			if err != nil {
				t.Fatalf("get reply: %v", err)
			}
			if !got.Answered || got.AnsweredVia != "web" {
				t.Fatalf("answered=%v via=%q, want true/web", got.Answered, got.AnsweredVia)
			}
			// selected serializes as [] (never null) per the wire contract.
			want := tc.ans
			if want.Selected == nil {
				want.Selected = []string{}
			}
			if !reflect.DeepEqual(*got.AskAnswer, want) {
				t.Fatalf("answer round-trip = %+v, want %+v", *got.AskAnswer, want)
			}
		})
	}

	t.Run("re-answer overwrites", func(t *testing.T) {
		askID := newAsk(false)
		if err := s.AnswerAsk(ctx, askID, AskAnswer{Selected: []string{"A"}}, "web"); err != nil {
			t.Fatal(err)
		}
		if err := s.AnswerAsk(ctx, askID, AskAnswer{Selected: []string{"B"}}, "askuserquestion"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetReply(ctx, askID)
		if got.AskAnswer.Selected[0] != "B" || got.AnsweredVia != "askuserquestion" {
			t.Fatalf("got %+v via %q, want B via askuserquestion", got.AskAnswer, got.AnsweredVia)
		}
	})

	t.Run("question target rejected", func(t *testing.T) {
		qid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "Q?"})
		if err := s.AnswerAsk(ctx, qid, AskAnswer{Selected: []string{"A"}}, "web"); err == nil {
			t.Fatal("want error answering a question via AnswerAsk")
		}
	})

	t.Run("web answer blocked once submitted, drain still works", func(t *testing.T) {
		openID := newAsk(false)
		if err := s.AnswerAskIfOpen(ctx, openID, AskAnswer{Selected: []string{"A"}}, "web"); err != nil {
			t.Fatalf("open review: %v", err)
		}
		frozenID := newAsk(false)
		setReviewStatus(ctx, t, s, id, "submitted")
		if err := s.AnswerAskIfOpen(ctx, frozenID, AskAnswer{Selected: []string{"A"}}, "web"); !errors.Is(err, ErrReviewNotOpen) {
			t.Fatalf("err=%v, want ErrReviewNotOpen", err)
		}
		if err := s.AnswerAsk(ctx, frozenID, AskAnswer{Selected: []string{"B"}}, "askuserquestion"); err != nil {
			t.Fatalf("drain on submitted review: %v", err)
		}
	})
}

func TestAnswerQuestion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, id, "main", "HEAD", "/p", "[]", "")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	qid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "Q?"})
	if err := s.AnswerQuestion(ctx, qid, "because", "web"); err != nil {
		t.Fatalf("answer question: %v", err)
	}
	got, _ := s.GetReply(ctx, qid)
	if !got.Answered || got.Answer != "because" || got.AnsweredVia != "web" {
		t.Fatalf("got answered=%v answer=%q via=%q", got.Answered, got.Answer, got.AnsweredVia)
	}

	aid, _, _ := s.CreateReply(ctx, Reply{
		CommentID: cid, Origin: "claude", Kind: "ask", Body: "Q",
		Ask: &Ask{Options: []AskOption{{Label: "A"}}},
	})
	if err := s.AnswerQuestion(ctx, aid, "text", "web"); err == nil {
		t.Fatal("want error answering an ask via AnswerQuestion")
	}
	if err := s.AnswerQuestion(ctx, 99999, "text", "web"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: err=%v, want ErrNotFound", err)
	}
}

func TestListOpenQuestionsIncludesAsk(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "base0")
	v, _ := s.CreateVersion(ctx, id, "main", "HEAD", "/p", "[]", "")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 3, EndLine: 3, Body: "hm"})

	qid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "free-form?"})
	ask := &Ask{Options: []AskOption{{Label: "A"}, {Label: "B", Description: "alt"}}}
	aid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "pick", Ask: ask})
	answeredID, _, _ := s.CreateReply(ctx, Reply{
		CommentID: cid, Origin: "claude", Kind: "ask", Body: "done",
		Ask: &Ask{Options: []AskOption{{Label: "X"}}},
	})
	if err := s.AnswerAsk(ctx, answeredID, AskAnswer{Selected: []string{"X"}}, "web"); err != nil {
		t.Fatal(err)
	}

	open, err := s.ListOpenQuestions(ctx, id)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("got %d open questions, want 2 (answered ask excluded)", len(open))
	}
	if open[0].ReplyID != qid || open[0].Ask != nil {
		t.Fatalf("first = %+v, want plain question %d", open[0], qid)
	}
	if open[1].ReplyID != aid || open[1].Ask == nil || !reflect.DeepEqual(*open[1].Ask, *ask) {
		t.Fatalf("second = %+v, want ask %d with decoded options", open[1], aid)
	}
}
