package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	ccevent "github.com/yasyf/cc-interact/event"

	"github.com/yasyf/cc-review/internal/store"
)

// backdateReview pushes a review's creation and every existing event to at, so
// staleness tests control the idle clock the sweeper reads.
func backdateReview(ctx context.Context, t *testing.T, s *Server, id string, at time.Time) {
	t.Helper()
	db := s.store.DB()
	if _, err := db.ExecContext(ctx, `UPDATE subjects SET created_at=? WHERE id=?`, at.Unix(), id); err != nil {
		t.Fatalf("backdate subject: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE events SET created_at=? WHERE subject_id=?`, at.Unix(), id); err != nil {
		t.Fatalf("backdate events: %v", err)
	}
}

// statusChangedPayloads returns the status field of each status.changed event
// on the review, in order.
func statusChangedPayloads(t *testing.T, s *Server, reviewID string) []string {
	t.Helper()
	events, err := s.cc.EventsSince(context.Background(), reviewID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		if e.Type != store.EventStatusChanged {
			continue
		}
		var p struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode status.changed payload: %v", err)
		}
		out = append(out, p.Status)
	}
	return out
}

func TestSweepStaleOpenExpiresIdleReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}

	if err := s.sweepStaleOpen(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	review, err := s.getReview(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "expired" {
		t.Fatalf("status = %q, want expired", review.Status)
	}
	// Never Detach: the subject stays bound so an explicit start can reopen it.
	if review.SessionID != "sA" || review.ClaudePID != 100 {
		t.Fatalf("expired review detached to %s/%d, want still bound to sA/100", review.SessionID, review.ClaudePID)
	}
	if got := statusChangedPayloads(t, s, started.ReviewID); len(got) != 1 || got[0] != "expired" {
		t.Fatalf("status.changed payloads = %v, want exactly [expired]", got)
	}
	if guard := s.handleGuardEdit(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo}); !guard.Allow {
		t.Fatalf("guard must lift once the review expires: %s", guard.Reason)
	}
}

func TestSweepStaleOpenSkipsFreshActivity(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}

	if err := s.sweepStaleOpen(ctx, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.reviewStatus(ctx, started.ReviewID); status != "open" {
		t.Fatalf("status = %q, want open", status)
	}
	if n := countEvents(t, s, started.ReviewID, store.EventStatusChanged); n != 0 {
		t.Fatalf("status.changed events = %d, want 0", n)
	}
	if guard := s.handleGuardEdit(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo}); guard.Allow {
		t.Fatal("guard must keep blocking a fresh open review")
	}
}

func TestSweepStaleOpenChannelEventsDontCountAsActivity(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	backdateReview(ctx, t, s, started.ReviewID, time.Now().Add(-2*time.Hour))
	// The incident shape: a fresh presence ping on a review whose real activity
	// stopped long ago must not reset the idle clock.
	emit(ctx, s.appendEvent, started.ReviewID, ccevent.OriginSystem, store.EventChannelChanged, started.Version,
		map[string]any{"connected": true})

	if err := s.sweepStaleOpen(ctx, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.reviewStatus(ctx, started.ReviewID); status != "expired" {
		t.Fatalf("status = %q, want expired: a presence ping must not reset the idle clock", status)
	}
}

func TestSweepStaleOpenNeverTouchesSubmitted(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	if err := s.resolver.Store.SetStatus(ctx, started.ReviewID, "submitted"); err != nil {
		t.Fatal(err)
	}

	if err := s.sweepStaleOpen(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.reviewStatus(ctx, started.ReviewID); status != "submitted" {
		t.Fatalf("status = %q, want submitted untouched", status)
	}
	if n := countEvents(t, s, started.ReviewID, store.EventStatusChanged); n != 0 {
		t.Fatalf("status.changed events = %d, want 0", n)
	}
}

func TestSweepStaleOpenIdempotent(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}

	for i := 0; i < 2; i++ {
		if err := s.sweepStaleOpen(ctx, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("sweep %d: %v", i+1, err)
		}
	}
	if n := countEvents(t, s, started.ReviewID, store.EventStatusChanged); n != 1 {
		t.Fatalf("status.changed events = %d, want exactly 1 across repeated sweeps", n)
	}
}

func TestHandleStartReopensExpiredReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	if err := s.resolver.Store.SetStatus(ctx, started.ReviewID, "expired"); err != nil {
		t.Fatal(err)
	}

	resumed := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !resumed.OK || !resumed.Resumed || resumed.ReviewID != started.ReviewID || resumed.Version != started.Version {
		t.Fatalf("resume: ok=%v resumed=%v id=%q version=%d err=%q, want dedup resume of %q version %d",
			resumed.OK, resumed.Resumed, resumed.ReviewID, resumed.Version, resumed.Error, started.ReviewID, started.Version)
	}
	if status, _ := s.reviewStatus(ctx, started.ReviewID); status != "open" {
		t.Fatalf("status = %q, want open: an explicit start reopens an expired review", status)
	}
	// The reopen must announce itself so a tab frozen on the expired banner thaws.
	if got := statusChangedPayloads(t, s, started.ReviewID); len(got) != 1 || got[0] != "open" {
		t.Fatalf("status.changed payloads = %v, want exactly [open]", got)
	}
	if guard := s.handleGuardEdit(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo}); guard.Allow {
		t.Fatal("guard must block again after reopening an expired review")
	}
}

func TestSessionRotationSkipsExpiredReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	if err := s.resolver.Store.SetStatus(ctx, started.ReviewID, "expired"); err != nil {
		t.Fatal(err)
	}

	// Rotation (same pid, new session id) must not rebind an expired review —
	// Policy.Active gates the pid-latest resume to open only.
	resp := s.handleSessionRecord(ctx, Request{Session: "sB", ClaudePID: 100, Cwd: repo})
	if !resp.OK {
		t.Fatalf("session-record failed: %s", resp.Error)
	}
	if _, ok, _ := s.resolver.Store.FindBySessionScope(ctx, "sB", root); ok {
		t.Fatal("rotated session must not be bound to an expired review")
	}
	review, err := s.getReview(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if review.SessionID != "sA" || review.ClaudePID != 100 {
		t.Fatalf("expired review rebound to %s/%d, want sA/100 untouched", review.SessionID, review.ClaudePID)
	}
	if guard := s.handleGuardEdit(ctx, Request{Session: "sB", ClaudePID: 100, Cwd: repo}); !guard.Allow {
		t.Fatalf("rotated window must not be blocked by its expired review: %s", guard.Reason)
	}
}

func TestHandleCloseOwnWindow(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	started := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}

	resp := s.handleClose(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !resp.OK {
		t.Fatalf("close: %s", resp.Error)
	}
	if len(resp.Closed) != 1 || resp.Closed[0].ID != started.ReviewID || resp.Closed[0].Status != "closed" || resp.Closed[0].Slug == "" {
		t.Fatalf("closed = %+v, want the window's review closed with its slug", resp.Closed)
	}
	review, err := s.getReview(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "closed" || review.SessionID != "" || review.ClaudePID != 0 {
		t.Fatalf("review = %+v, want closed and detached", review)
	}
	if got := statusChangedPayloads(t, s, started.ReviewID); len(got) != 1 || got[0] != "closed" {
		t.Fatalf("status.changed payloads = %v, want exactly [closed]", got)
	}
	if guard := s.handleGuardEdit(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo}); !guard.Allow {
		t.Fatalf("guard must lift after close: %s", guard.Reason)
	}

	// A closed review is never resumed: the next start creates fresh.
	restarted := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !restarted.OK || restarted.Resumed || restarted.ReviewID == started.ReviewID {
		t.Fatalf("restart: ok=%v resumed=%v id=%q, want a fresh review", restarted.OK, restarted.Resumed, restarted.ReviewID)
	}
}

func TestHandleCloseByRef(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	other, err := s.createReview(ctx, "sA", 100, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}

	// The repair path: a different window closes an abandoned review by slug.
	resp := s.handleClose(ctx, Request{Session: "sB", ClaudePID: 999, Cwd: repo, Ref: other.Slug})
	if !resp.OK {
		t.Fatalf("close by ref: %s", resp.Error)
	}
	if len(resp.Closed) != 1 || resp.Closed[0].Slug != other.Slug {
		t.Fatalf("closed = %+v, want %s", resp.Closed, other.Slug)
	}
	review, err := s.getReview(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "closed" || review.SessionID != "" || review.ClaudePID != 0 {
		t.Fatalf("review = %+v, want closed and detached", review)
	}
}

func TestHandleCloseNotOpen(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	sub, err := s.createReview(ctx, "sA", 100, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.resolver.Store.SetStatus(ctx, sub.ID, "submitted"); err != nil {
		t.Fatal(err)
	}

	resp := s.handleClose(ctx, Request{Session: "sB", ClaudePID: 999, Cwd: repo, Ref: sub.Slug})
	if resp.OK || !strings.Contains(resp.Error, "only an open or expired review can be closed") {
		t.Fatalf("close of submitted review: ok=%v err=%q, want the not-closable error", resp.OK, resp.Error)
	}
	if status, _ := s.reviewStatus(ctx, sub.ID); status != "submitted" {
		t.Fatalf("status = %q, want submitted untouched", status)
	}
	if n := countEvents(t, s, sub.ID, store.EventStatusChanged); n != 0 {
		t.Fatalf("status.changed events = %d, want 0", n)
	}
}

func TestHandleCloseNoReview(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)

	resp := s.handleClose(ctx, Request{Session: "nobody", ClaudePID: 12345, Cwd: repo})
	if resp.OK || !strings.Contains(resp.Error, "--stale") {
		t.Fatalf("close with no review: ok=%v err=%q, want an error mentioning --stale", resp.OK, resp.Error)
	}
}

func TestHandleCloseStale(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	idle := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !idle.OK {
		t.Fatalf("start idle: %s", idle.Error)
	}
	fresh := s.handleStart(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !fresh.OK {
		t.Fatalf("start fresh: %s", fresh.Error)
	}
	backdateReview(ctx, t, s, idle.ReviewID, time.Now().Add(-reviewIdleTTL-time.Hour))
	// A review the daemon's own sweeps expired earlier must be closed and
	// reported too — the whole point of the repair command.
	alreadyExpired, err := s.createReview(ctx, "sC", 300, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.resolver.Store.SetStatus(ctx, alreadyExpired.ID, "expired"); err != nil {
		t.Fatal(err)
	}

	resp := s.handleClose(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo, Stale: true})
	if !resp.OK {
		t.Fatalf("close --stale: %s", resp.Error)
	}
	if len(resp.Closed) != 2 {
		t.Fatalf("closed = %+v, want the idle and already-expired reviews", resp.Closed)
	}
	want := map[string]bool{idle.ReviewID: true, alreadyExpired.ID: true}
	for _, r := range resp.Closed {
		if !want[r.ID] || r.Status != "closed" {
			t.Fatalf("closed row = %+v, want status closed for %v", r, want)
		}
		// The report carries the pre-expiry idle anchor, not the timestamp of the
		// status.changed the sweep itself just emitted.
		if r.ID == idle.ReviewID && !r.LastActivity.Before(time.Now().Add(-reviewIdleTTL)) {
			t.Fatalf("idle review reported last activity %v, want the backdated pre-expiry anchor", r.LastActivity)
		}
		delete(want, r.ID)
	}
	for _, id := range []string{idle.ReviewID, alreadyExpired.ID} {
		if status, _ := s.reviewStatus(ctx, id); status != "closed" {
			t.Fatalf("review %s status = %q, want closed", id, status)
		}
	}
	if status, _ := s.reviewStatus(ctx, fresh.ReviewID); status != "open" {
		t.Fatalf("fresh status = %q, want open untouched", status)
	}
}

func TestHandleList(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	writeFile(t, repo, "pending.go", "package p\nvar Pending int\n")

	older := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !older.OK {
		t.Fatalf("start older: %s", older.Error)
	}
	backdateReview(ctx, t, s, older.ReviewID, time.Now().Add(-time.Hour))
	newer := s.handleStart(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !newer.OK {
		t.Fatalf("start newer: %s", newer.Error)
	}
	submitted, err := s.createReview(ctx, "sC", 300, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.resolver.Store.SetStatus(ctx, submitted.ID, "submitted"); err != nil {
		t.Fatal(err)
	}
	expired, err := s.createReview(ctx, "sD", 400, root, "main", "base0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.resolver.Store.SetStatus(ctx, expired.ID, "expired"); err != nil {
		t.Fatal(err)
	}
	backdateReview(ctx, t, s, expired.ID, time.Now().Add(-3*time.Hour))

	resp := s.handleList(ctx, Request{Session: "sB", ClaudePID: 200, Cwd: repo})
	if !resp.OK {
		t.Fatalf("list: %s", resp.Error)
	}
	wantIDs := []string{expired.ID, older.ReviewID, newer.ReviewID}
	wantStatuses := []string{"expired", "open", "open"}
	if len(resp.Reviews) != 3 {
		t.Fatalf("reviews = %+v, want %v ordered by last activity", resp.Reviews, wantIDs)
	}
	for i, r := range resp.Reviews {
		if r.ID != wantIDs[i] || r.Status != wantStatuses[i] {
			t.Fatalf("review %d = %s (%s), want %s (%s)", i, r.ID, r.Status, wantIDs[i], wantStatuses[i])
		}
		if r.Scope != root || r.Slug == "" {
			t.Fatalf("review row = %+v, want scope %s and a non-empty slug", r, root)
		}
	}
}
