package store

import (
	"context"
	"path/filepath"
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

	r, err := s.CreateReview(ctx, "sess-1", "/repo/a")
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
	if _, err := s.CreateReview(ctx, "", "/repo/b"); err != nil {
		t.Fatalf("session-less review 1: %v", err)
	}
	if _, err := s.CreateReview(ctx, "", "/repo/c"); err != nil {
		t.Fatalf("session-less review 2: %v", err)
	}

	open, ok, err := s.FindLatestOpenReviewByRepo(ctx, "/repo/a")
	if err != nil || !ok || open.ID != r.ID {
		t.Fatalf("latest open by repo: ok=%v id=%q err=%v", ok, open.ID, err)
	}
}

func TestVersionNumbersAreMonotonic(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", "/repo")

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
	r, _ := s.CreateReview(ctx, "s", "/repo")
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

func TestAppendEventSeqAndOriginFilter(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r, _ := s.CreateReview(ctx, "s", "/repo")

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
	r, _ := s.CreateReview(ctx, "s", "/repo")

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

