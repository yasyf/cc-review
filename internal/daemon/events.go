package daemon

import (
	"context"
	"fmt"

	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

// Subscribe exposes the bus to the HTTP plane (satisfies httpapi.Backend).
func (s *Server) Subscribe(reviewID string) (<-chan struct{}, func()) {
	return s.bus.Subscribe(reviewID)
}

// Attach registers a named SSE stream consumer for a review and returns its
// detach (satisfies httpapi.Backend).
func (s *Server) Attach(reviewID, consumer string, claudePID int) func() {
	return s.activity.Attach(reviewID, consumer, claudePID)
}

// ClaudeConnected reports whether any Claude-side stream consumer (watch or
// channel — the browser never registers) is attached to the review, with a
// grace window that papers over reconnect blips. Pid-agnostic: it answers the
// browser's "can the AI bar act?", not window ownership. Satisfies
// httpapi.Backend.
func (s *Server) ClaudeConnected(reviewID string) bool {
	return s.activity.AttachedWithin(reviewID, attachGrace)
}

// AppendEvent is the single chokepoint through which every event enters the log,
// from any origin: it persists the event, then publishes the wakeup. Persisting
// before publishing guarantees a woken consumer can read the row. Satisfies
// httpapi.Backend.
func (s *Server) AppendEvent(ctx context.Context, e *store.Event) (int64, error) {
	seq, err := s.store.AppendEvent(ctx, e)
	if err != nil {
		return 0, fmt.Errorf("append %s event: %w", e.Type, err)
	}
	s.bus.Publish(e.ReviewID)
	return seq, nil
}

// reconcileChannelEvents closes out channel.changed state orphaned by a daemon
// death: the SSE handler's detach defer and debounce timer die with the
// process, so a log whose last word is connected:true would replay as a live
// channel forever. It runs once at boot, BEFORE the HTTP plane accepts
// attaches — Activity is empty, so every stale connected:true is provably
// false and the closing event cannot race a fresh attach.
func (s *Server) reconcileChannelEvents(ctx context.Context) error {
	ids, err := s.store.StaleConnectedReviews(ctx)
	if err != nil {
		return fmt.Errorf("reconcile channel events: %w", err)
	}
	for _, id := range ids {
		v, ok, err := s.store.LatestVersion(ctx, id)
		if err != nil {
			return fmt.Errorf("reconcile channel events: %w", err)
		}
		if !ok {
			return fmt.Errorf("reconcile channel events: review %s has channel.changed events but no versions", id)
		}
		if _, err := s.AppendEvent(ctx, &store.Event{
			ReviewID: id, Origin: store.OriginSystem, Type: store.EventChannelChanged, VersionNumber: v.VersionNumber,
			Payload: wire.Event(store.EventChannelChanged, v.VersionNumber, map[string]any{"connected": false}),
		}); err != nil {
			return err
		}
	}
	return nil
}
