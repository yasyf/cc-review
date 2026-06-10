package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

func TestReconcileChannelEventsAtBoot(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	_, started := startedReview(t, s, repo)

	// A log with no channel.changed events has nothing to reconcile.
	if err := s.reconcileChannelEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countEvents(t, s, started.ReviewID, store.EventChannelChanged); got != 0 {
		t.Fatalf("reconcile on a clean log emitted %d channel.changed events", got)
	}

	// A daemon death while a consumer was attached leaves connected:true as the
	// log's last word; a second version lands before the next boot.
	if _, err := s.AppendEvent(ctx, &store.Event{
		ReviewID: started.ReviewID, Origin: store.OriginSystem, Type: store.EventChannelChanged, VersionNumber: started.Version,
		Payload: wire.Event(store.EventChannelChanged, started.Version, map[string]any{"connected": true}),
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "a.go", "package a\nfunc Changed() {}\n")
	second := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !second.OK {
		t.Fatalf("second start: %s", second.Error)
	}

	if err := s.reconcileChannelEvents(ctx); err != nil {
		t.Fatal(err)
	}
	events := eventsOfType(t, s, started.ReviewID, store.EventChannelChanged, false)
	if len(events) != 2 {
		t.Fatalf("channel.changed events = %d, want the stale true plus the boot false", len(events))
	}
	closing := events[1]
	if closing.Origin != store.OriginSystem || closing.VersionNumber != second.Version {
		t.Fatalf("closing event origin=%s version=%d, want system on version %d",
			closing.Origin, closing.VersionNumber, second.Version)
	}
	var payload struct {
		Connected     bool `json:"connected"`
		VersionNumber int  `json:"version_number"`
	}
	if err := json.Unmarshal(closing.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Connected || payload.VersionNumber != second.Version {
		t.Fatalf("closing payload = %+v, want connected:false on version %d", payload, second.Version)
	}

	// Idempotent: a log already closed with connected:false is left alone.
	if err := s.reconcileChannelEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countEvents(t, s, started.ReviewID, store.EventChannelChanged); got != 2 {
		t.Fatalf("second reconcile grew the log to %d channel.changed events", got)
	}
}
