package daemon

import (
	"context"
	"fmt"

	"github.com/yasyf/cc-review/internal/store"
)

// Subscribe exposes the bus to the HTTP plane (satisfies httpapi.Backend).
func (s *Server) Subscribe(reviewID string) (<-chan struct{}, func()) {
	return s.bus.Subscribe(reviewID)
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
