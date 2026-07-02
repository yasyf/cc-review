package store

import (
	"context"
	"testing"
	"time"

	ccevent "github.com/yasyf/cc-interact/event"
)

// appendEventAt appends an event and backdates it: AppendEvent stamps now, and
// the idle computation under test reads events.created_at.
func appendEventAt(ctx context.Context, t *testing.T, s *Store, id, typ string, at time.Time) {
	t.Helper()
	if _, err := s.cc.AppendEvent(ctx, &ccevent.Event{
		SubjectID: id, Origin: ccevent.OriginHuman, Type: typ, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("append %s: %v", typ, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE events SET created_at=? WHERE subject_id=? AND seq=(SELECT MAX(seq) FROM events WHERE subject_id=?)`,
		at.Unix(), id, id); err != nil {
		t.Fatalf("backdate %s: %v", typ, err)
	}
}

func setCreatedAt(ctx context.Context, t *testing.T, s *Store, id string, at time.Time) {
	t.Helper()
	if _, err := s.db.ExecContext(ctx, `UPDATE subjects SET created_at=? WHERE id=?`, at.Unix(), id); err != nil {
		t.Fatalf("backdate subject: %v", err)
	}
}

func TestStaleOpenReviews(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	var (
		cutoff  = time.Unix(1_750_000_000, 0)
		ancient = cutoff.Add(-72 * time.Hour)
		old     = cutoff.Add(-time.Hour)
		fresh   = cutoff.Add(time.Hour)
	)

	// Real activity stopped before the cutoff; a fresh presence ping must not
	// keep it alive — this is the incident shape.
	presencePinged := seedReview(ctx, t, s, "s1", 0, "/repo/a", "main", "b")
	setCreatedAt(ctx, t, s, presencePinged, ancient)
	appendEventAt(ctx, t, s, presencePinged, EventCommentCreated, old)
	appendEventAt(ctx, t, s, presencePinged, EventChannelChanged, fresh)

	active := seedReview(ctx, t, s, "s2", 0, "/repo/b", "main", "b")
	setCreatedAt(ctx, t, s, active, ancient)
	appendEventAt(ctx, t, s, active, EventCommentCreated, fresh)

	// No events at all: created_at anchors the idle clock.
	eventless := seedReview(ctx, t, s, "s3", 0, "/repo/c", "main", "b")
	setCreatedAt(ctx, t, s, eventless, old)

	freshEventless := seedReview(ctx, t, s, "s4", 0, "/repo/d", "main", "b")
	setCreatedAt(ctx, t, s, freshEventless, fresh)

	for _, status := range []string{"submitted", "closed", "expired"} {
		id := seedReview(ctx, t, s, "s-"+status, 0, "/repo/"+status, "main", "b")
		setCreatedAt(ctx, t, s, id, ancient)
		setReviewStatus(ctx, t, s, id, status)
	}

	got, err := s.StaleOpenReviews(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Time{presencePinged: old, eventless: old}
	if len(got) != 2 {
		t.Fatalf("stale reviews = %+v, want exactly {%s, %s}", got, presencePinged, eventless)
	}
	for _, r := range got {
		wantActivity, ok := want[r.ID]
		if !ok {
			t.Fatalf("unexpected stale review %s (%+v)", r.ID, r)
		}
		if !r.LastActivity.Equal(wantActivity) {
			t.Fatalf("review %s last activity = %v, want %v", r.ID, r.LastActivity, wantActivity)
		}
		delete(want, r.ID)
	}
}

func TestListReviews(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	base := time.Unix(1_750_000_000, 0)

	newer := seedReview(ctx, t, s, "s1", 0, "/repo/a", "main", "b")
	setCreatedAt(ctx, t, s, newer, base.Add(-time.Hour))
	appendEventAt(ctx, t, s, newer, EventFileStates, base)

	expired := seedReview(ctx, t, s, "s2", 0, "/repo/b", "main", "b")
	setCreatedAt(ctx, t, s, expired, base.Add(-48*time.Hour))
	setReviewStatus(ctx, t, s, expired, "expired")

	submitted := seedReview(ctx, t, s, "s3", 0, "/repo/c", "main", "b")
	setReviewStatus(ctx, t, s, submitted, "submitted")
	closed := seedReview(ctx, t, s, "s4", 0, "/repo/d", "main", "b")
	setReviewStatus(ctx, t, s, closed, "closed")

	got, err := s.ListReviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != expired || got[1].ID != newer {
		t.Fatalf("reviews = %+v, want [%s, %s] ordered by last activity", got, expired, newer)
	}
	if got[0].Status != "expired" || got[1].Status != "open" {
		t.Fatalf("statuses = %s, %s, want expired, open", got[0].Status, got[1].Status)
	}
	if got[0].Scope != "/repo/b" || got[1].Scope != "/repo/a" {
		t.Fatalf("scopes = %s, %s, want /repo/b, /repo/a", got[0].Scope, got[1].Scope)
	}
	if !got[0].LastActivity.Equal(base.Add(-48*time.Hour)) || !got[1].LastActivity.Equal(base) {
		t.Fatalf("last activity = %v, %v, want %v, %v",
			got[0].LastActivity, got[1].LastActivity, base.Add(-48*time.Hour), base)
	}
	if got[1].Slug == "" {
		t.Fatal("review slug is empty")
	}
}

func TestSubjectStatusCAS(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		from       string
		transition func(*Store, string) (bool, error)
		swapped    bool
		wantStatus string
	}{
		{"expire open", "open", expire, true, "expired"},
		{"expire submitted", "submitted", expire, false, "submitted"},
		{"expire expired", "expired", expire, false, "expired"},
		{"expire closed", "closed", expire, false, "closed"},
		{"close open", "open", closeRev, true, "closed"},
		{"close expired", "expired", closeRev, true, "closed"},
		{"close submitted", "submitted", closeRev, false, "submitted"},
		{"close closed", "closed", closeRev, false, "closed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "b")
			setReviewStatus(ctx, t, s, id, tc.from)

			swapped, err := tc.transition(s, id)
			if err != nil {
				t.Fatal(err)
			}
			if swapped != tc.swapped {
				t.Fatalf("swapped = %v, want %v", swapped, tc.swapped)
			}
			r, err := s.GetReview(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", r.Status, tc.wantStatus)
			}
		})
	}
}

// expire uses a future cutoff so the CAS's idle re-check always passes and the
// table exercises the status arm alone; the idle arm is pinned separately.
func expire(s *Store, id string) (bool, error) {
	return s.ExpireReview(context.Background(), id, time.Now().Add(time.Hour))
}

func closeRev(s *Store, id string) (bool, error) {
	return s.CloseReview(context.Background(), id)
}

func TestExpireReviewRechecksIdleness(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	id := seedReview(ctx, t, s, "s", 0, "/repo", "main", "b")
	appendEventAt(ctx, t, s, id, EventCommentCreated, time.Now())

	// Fresh activity landed after a sweep's scan would have listed the review:
	// the CAS must abort instead of expiring an active review.
	swapped, err := s.ExpireReview(ctx, id, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if swapped {
		t.Fatal("expired a review with activity newer than the cutoff")
	}
	if r, _ := s.GetReview(ctx, id); r.Status != "open" {
		t.Fatalf("status = %q, want open", r.Status)
	}
}

func TestCloseAndDetach(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	subjects := newSubjectStoreForTest(s)

	open := seedReview(ctx, t, s, "s1", 100, "/repo/a", "main", "b")
	swapped, err := s.CloseAndDetach(ctx, subjects, open)
	if err != nil || !swapped {
		t.Fatalf("close open review: swapped=%v err=%v", swapped, err)
	}
	sub, err := subjects.Get(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != "closed" || sub.SessionID != "" || sub.ClaudePID != 0 {
		t.Fatalf("subject = %+v, want closed and detached", sub)
	}

	submitted := seedReview(ctx, t, s, "s2", 200, "/repo/b", "main", "b")
	setReviewStatus(ctx, t, s, submitted, "submitted")
	swapped, err = s.CloseAndDetach(ctx, subjects, submitted)
	if err != nil || swapped {
		t.Fatalf("close submitted review: swapped=%v err=%v, want no-op", swapped, err)
	}
	sub, err = subjects.Get(ctx, submitted)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != "submitted" || sub.SessionID != "s2" || sub.ClaudePID != 200 {
		t.Fatalf("subject = %+v, want submitted and still attached", sub)
	}
}
