package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const keepaliveInterval = 20 * time.Second

// handleEvents streams a review's event log as Server-Sent Events. ?session=
// is a review ref — the browser sends the slug, the Claude-side stream
// consumers the full id — resolved here to the canonical id, which is what
// keys the Bus and the events table. The browser omits exclude_origin and sees
// every origin (including Claude's replies); the Claude-side stream consumers
// pass exclude_origin=claude to drop their own echo. Resume is via
// Last-Event-ID (header, or the ?last_event_id= query fallback for native
// EventSource which cannot set headers on the initial request).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("session")
	if ref == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}
	review, err := s.store.GetReviewByRef(r.Context(), ref)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	reviewID := review.ID
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	excludeClaude := r.URL.Query().Get("exclude_origin") == "claude"
	// Named stream consumers (watch, channel) register their presence; the
	// browser sends no consumer param and is never registered.
	if consumer := r.URL.Query().Get("consumer"); consumer != "" {
		defer s.backend.Attach(reviewID, consumer)()
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	cursor := parseCursor(r)

	// Subscribe BEFORE the first query so an event committed during replay is not
	// lost between the gap query and the park (the cap-1 buffer retains the edge).
	signal, unsub := s.backend.Subscribe(reviewID)
	defer unsub()

	ctx := r.Context()
	cursor = s.flushSince(ctx, w, flusher, reviewID, cursor, excludeClaude)
	io.WriteString(w, ": connected\n\n") // prove liveness + flush proxies
	flusher.Flush()

	ka := time.NewTicker(keepaliveInterval)
	defer ka.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ka.C:
			io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		case <-signal:
			cursor = s.flushSince(ctx, w, flusher, reviewID, cursor, excludeClaude)
		}
	}
}

// flushSince writes one SSE frame per event with seq greater than cursor and
// returns the new high-water cursor. One query per wake; no DB handle is held
// across the select.
func (s *Server) flushSince(ctx context.Context, w io.Writer, fl http.Flusher, reviewID string, cursor int64, excludeClaude bool) int64 {
	evs, err := s.store.EventsSince(ctx, reviewID, cursor, excludeClaude)
	if err != nil {
		return cursor
	}
	for _, e := range evs {
		// No `event:` field: native EventSource delivers only default-type frames
		// to onmessage, which is how the browser consumes the stream. The frame's
		// type lives inside the JSON payload instead.
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, e.Payload)
		if e.Seq > cursor {
			cursor = e.Seq
		}
	}
	if len(evs) > 0 {
		fl.Flush()
	}
	return cursor
}

func parseCursor(r *http.Request) int64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	if v := r.URL.Query().Get("last_event_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}
