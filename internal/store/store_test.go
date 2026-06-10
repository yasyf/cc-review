package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestReviewResolution(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	r, err := s.CreateReview(ctx, "sess-1", "/repo/a", "main")
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	if r.Status != "open" {
		t.Fatalf("status = %q, want open", r.Status)
	}

	got, ok, err := s.FindReviewBySessionRepo(ctx, "sess-1", "/repo/a")
	if err != nil || !ok {
		t.Fatalf("find by session/repo: ok=%v err=%v", ok, err)
	}
	if got.ID != r.ID {
		t.Fatalf("found id %q, want %q", got.ID, r.ID)
	}

	if _, ok, _ := s.FindReviewBySessionRepo(ctx, "sess-2", "/repo/a"); ok {
		t.Fatal("different session should not match")
	}

	// A session-less review must not collide with another on the partial index.
	if _, err := s.CreateReview(ctx, "", "/repo/b", "main"); err != nil {
		t.Fatalf("session-less review 1: %v", err)
	}
	if _, err := s.CreateReview(ctx, "", "/repo/c", "main"); err != nil {
		t.Fatalf("session-less review 2: %v", err)
	}

	open, ok, err := s.FindLatestOpenReviewByRepo(ctx, "/repo/a")
	if err != nil || !ok || open.ID != r.ID {
		t.Fatalf("latest open by repo: ok=%v id=%q err=%v", ok, open.ID, err)
	}
}

func TestCreateReviewRecordsInitialBinding(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	sessioned, _ := s.CreateReview(ctx, "s1", "/repo/a", "main")
	hist, err := s.ListReviewSessions(ctx, sessioned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Source != "create" || hist[0].SessionID != "s1" {
		t.Fatalf("sessioned create history = %+v, want one create:s1 row", hist)
	}

	sessionless, _ := s.CreateReview(ctx, "", "/repo/b", "main")
	hist, _ = s.ListReviewSessions(ctx, sessionless.ID)
	if len(hist) != 0 {
		t.Fatalf("sessionless create should record no binding, got %+v", hist)
	}
}

func TestReparentReviewSession(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s1", "/repo", "main")

	if err := s.ReparentReviewSession(ctx, r.ID, "s2", "adopt"); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	got, _ := s.GetReview(ctx, r.ID)
	if got.SessionID != "s2" {
		t.Fatalf("session_id = %q, want s2", got.SessionID)
	}
	if _, ok, _ := s.FindReviewBySessionRepo(ctx, "s2", "/repo"); !ok {
		t.Fatal("s2 should now match")
	}
	if _, ok, _ := s.FindReviewBySessionRepo(ctx, "s1", "/repo"); ok {
		t.Fatal("s1 should no longer match")
	}
}

func TestReparentAppendsAuditRowPerRebind(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s1", "/repo", "main")

	if err := s.ReparentReviewSession(ctx, r.ID, "s2", "adopt"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReparentReviewSession(ctx, r.ID, "s3", "session-start"); err != nil {
		t.Fatal(err)
	}
	hist, err := s.ListReviewSessions(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ session, source string }{
		{"s1", "create"}, {"s2", "adopt"}, {"s3", "session-start"},
	}
	if len(hist) != len(want) {
		t.Fatalf("got %d history rows, want %d: %+v", len(hist), len(want), hist)
	}
	for i, w := range want {
		if hist[i].SessionID != w.session || hist[i].Source != w.source {
			t.Fatalf("row %d = %s/%s, want %s/%s", i, hist[i].SessionID, hist[i].Source, w.session, w.source)
		}
	}
}

func TestReparentCollisionFailsAtomically(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	a, _ := s.CreateReview(ctx, "s1", "/repo", "main")
	if _, err := s.CreateReview(ctx, "s2", "/repo", "main"); err != nil {
		t.Fatal(err)
	}

	// s2 already owns a review in /repo: the unique index must reject the rebind.
	if err := s.ReparentReviewSession(ctx, a.ID, "s2", "adopt"); err == nil {
		t.Fatal("reparent onto an occupied (session, repo) slot should fail")
	}
	got, _ := s.GetReview(ctx, a.ID)
	if got.SessionID != "s1" {
		t.Fatalf("binding changed despite failed tx: %q", got.SessionID)
	}
	hist, _ := s.ListReviewSessions(ctx, a.ID)
	if len(hist) != 1 {
		t.Fatalf("audit row leaked from a rolled-back tx: %+v", hist)
	}
}

func TestReparentUnknownReviewErrNotFound(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.ReparentReviewSession(ctx, "nope", "s1", "adopt"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestVersionNumbersAreMonotonic(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")

	for want := 1; want <= 3; want++ {
		v, err := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p.patch", "[]")
		if err != nil {
			t.Fatalf("create version: %v", err)
		}
		if v.VersionNumber != want {
			t.Fatalf("version number = %d, want %d", v.VersionNumber, want)
		}
	}
	latest, ok, _ := s.LatestVersion(ctx, r.ID)
	if !ok || latest.VersionNumber != 3 {
		t.Fatalf("latest = %d (ok=%v), want 3", latest.VersionNumber, ok)
	}
}

func TestReplyDedupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")
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
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")
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

func TestAppendEventSeqAndOriginFilter(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")

	want := []struct {
		origin string
		seq    int64
	}{{"user", 1}, {"claude", 2}, {"user", 3}}
	for _, w := range want {
		e := &Event{ReviewID: r.ID, Origin: w.origin, Type: "t", Payload: []byte(`{"x":1}`)}
		seq, err := s.AppendEvent(ctx, e)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if seq != w.seq {
			t.Fatalf("seq = %d, want %d", seq, w.seq)
		}
	}

	all, _ := s.EventsSince(ctx, r.ID, 0, false)
	if len(all) != 3 {
		t.Fatalf("all events = %d, want 3", len(all))
	}
	noClaude, _ := s.EventsSince(ctx, r.ID, 0, true)
	if len(noClaude) != 2 {
		t.Fatalf("excludeClaude events = %d, want 2", len(noClaude))
	}
	for _, e := range noClaude {
		if e.Origin == "claude" {
			t.Fatal("claude event leaked through the filter")
		}
	}

	since := all[1].Seq // 2
	tail, _ := s.EventsSince(ctx, r.ID, since, false)
	if len(tail) != 1 || tail[0].Seq != 3 {
		t.Fatalf("events since %d = %+v, want only seq 3", since, tail)
	}
}

func TestEventDedupReturnsExistingSeq(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")

	first, _ := s.AppendEvent(ctx, &Event{ReviewID: r.ID, Origin: "user", Type: "t", DedupKey: "dk"})
	second, _ := s.AppendEvent(ctx, &Event{ReviewID: r.ID, Origin: "user", Type: "t", DedupKey: "dk"})
	if first != second {
		t.Fatalf("dedup seq mismatch: %d vs %d", first, second)
	}
	all, _ := s.EventsSince(ctx, r.ID, 0, false)
	if len(all) != 1 {
		t.Fatalf("got %d events, want 1 (deduped)", len(all))
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
	r, err := s.CreateReview(ctx, "s", "/repo", "feat/login")
	if err != nil {
		t.Fatal(err)
	}
	if want := "feat--login--" + r.ID[:8]; r.Slug != want {
		t.Fatalf("slug = %q, want %q", r.Slug, want)
	}

	bySlug, err := s.GetReviewByRef(ctx, r.Slug)
	if err != nil || bySlug.ID != r.ID {
		t.Fatalf("by slug: id=%q err=%v, want %q", bySlug.ID, err, r.ID)
	}
	byID, err := s.GetReviewByRef(ctx, r.ID)
	if err != nil || byID.Slug != r.Slug {
		t.Fatalf("by id: slug=%q err=%v, want %q", byID.Slug, err, r.Slug)
	}
	if _, err := s.GetReviewByRef(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("unknown ref err = %v, want ErrNotFound", err)
	}
}

func TestAskReplyRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	ask := &Ask{Header: "Approach", MultiSelect: true, Options: []AskOption{
		{Label: "Keep as-is", Description: "minimal churn"},
		{Label: "Extract a helper", Preview: "func helper() {}"},
	}}
	id, inserted, err := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "Which?", Ask: ask})
	if err != nil || !inserted {
		t.Fatalf("create ask: ins=%v err=%v", inserted, err)
	}

	got, err := s.GetReply(ctx, id)
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
		{"duplicate labels", Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "x",
			Ask: &Ask{Options: []AskOption{{Label: "A"}, {Label: "A"}}}}},
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
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	newAsk := func(multi bool) int64 {
		id, _, err := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "Q",
			Ask: &Ask{MultiSelect: multi, Options: []AskOption{{Label: "A"}, {Label: "B"}}}})
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
			id := newAsk(tc.multi)
			err := s.AnswerAsk(ctx, id, tc.ans, "web")
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("answer ask: %v", err)
			}
			got, err := s.GetReply(ctx, id)
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
		id := newAsk(false)
		if err := s.AnswerAsk(ctx, id, AskAnswer{Selected: []string{"A"}}, "web"); err != nil {
			t.Fatal(err)
		}
		if err := s.AnswerAsk(ctx, id, AskAnswer{Selected: []string{"B"}}, "askuserquestion"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetReply(ctx, id)
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
		if err := s.SetReviewStatus(ctx, r.ID, "submitted"); err != nil {
			t.Fatal(err)
		}
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
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1})

	qid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "Q?"})
	if err := s.AnswerQuestion(ctx, qid, "because", "web"); err != nil {
		t.Fatalf("answer question: %v", err)
	}
	got, _ := s.GetReply(ctx, qid)
	if !got.Answered || got.Answer != "because" || got.AnsweredVia != "web" {
		t.Fatalf("got answered=%v answer=%q via=%q", got.Answered, got.Answer, got.AnsweredVia)
	}

	aid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "Q",
		Ask: &Ask{Options: []AskOption{{Label: "A"}}}})
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
	r, _ := s.CreateReview(ctx, "s", "/repo", "main")
	v, _ := s.CreateVersion(ctx, r.ID, "main", "HEAD", "/p", "[]")
	cid, _ := s.CreateComment(ctx, Comment{VersionID: v.ID, FilePath: "a.go", Side: "additions", StartLine: 3, EndLine: 3, Body: "hm"})

	qid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "question", Body: "free-form?"})
	ask := &Ask{Options: []AskOption{{Label: "A"}, {Label: "B", Description: "alt"}}}
	aid, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "pick", Ask: ask})
	answeredID, _, _ := s.CreateReply(ctx, Reply{CommentID: cid, Origin: "claude", Kind: "ask", Body: "done",
		Ask: &Ask{Options: []AskOption{{Label: "X"}}}})
	if err := s.AnswerAsk(ctx, answeredID, AskAnswer{Selected: []string{"X"}}, "web"); err != nil {
		t.Fatal(err)
	}

	open, err := s.ListOpenQuestions(ctx, r.ID)
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
