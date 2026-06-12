package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/yasyf/cc-review/internal/store"
)

const keepaliveInterval = 20 * time.Second

// channelDownDebounce is how long after a watch/channel detach the handler
// waits before re-reading the connected predicate. It must outlast the
// backend's attach grace plus the consumers' reconnect delay, so a transient
// drop never persists a connected:false event.
const channelDownDebounce = 15 * time.Second

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
	// Named stream consumers (watch, channel) register their presence with
	// their window pid; the browser sends neither param and is never
	// registered. An absent claude_pid is a pid-less manual watch (0), not an
	// error; garbage is.
	consumer := r.URL.Query().Get("consumer")
	claudePID := 0
	if v := r.URL.Query().Get("claude_pid"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			http.Error(w, "bad claude_pid", http.StatusBadRequest)
			return
		}
		claudePID = n
	}
	// A named consumer's attach/detach drives channel.changed on transitions of
	// the connected predicate: a first attach emits connected, and a detach that
	// outlives the debounce (no consumer came back) emits disconnected. The
	// event is persisted (origin system) and last-wins on replay. Two
	// near-zero-width races are accepted under last-wins: concurrent first
	// attaches can double-emit connected:true (idempotent), and an attach
	// landing between the debounce predicate check and the false emit briefly
	// inverts to false until the next transition. A daemon death loses the
	// detach defer and the debounce timer entirely; the stale connected:true it
	// leaves behind is reconciled at the next daemon boot
	// (daemon.Server.reconcileChannelEvents). Named consumers never receive
	// channel.changed frames — only the browser does (it renders the
	// Claude-connected dot); the log keeps the rows for boot reconciliation.
	if consumer != "" {
		wasConnected := s.backend.ClaudeConnected(reviewID)
		detach := s.backend.Attach(reviewID, consumer, claudePID)
		if !wasConnected {
			s.emitChannelChanged(r.Context(), reviewID, true)
		}
		defer func() {
			detach()
			time.AfterFunc(channelDownDebounce, func() {
				if !s.backend.ClaudeConnected(reviewID) {
					s.emitChannelChanged(context.Background(), reviewID, false)
				}
			})
		}()
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
	cursor = s.flushSince(ctx, w, flusher, reviewID, cursor, excludeClaude, consumer != "")
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
			cursor = s.flushSince(ctx, w, flusher, reviewID, cursor, excludeClaude, consumer != "")
		}
	}
}

// flushSince writes one SSE frame per event with seq greater than cursor and
// returns the new high-water cursor. named drops channel.changed frames — the
// connectivity flip a named consumer caused must not wake it — but the cursor
// advances past skipped rows too, so a wake never re-queries the filtered
// tail. One query per wake; no DB handle is held across the select.
func (s *Server) flushSince(ctx context.Context, w io.Writer, fl http.Flusher, reviewID string, cursor int64, excludeClaude, named bool) int64 {
	evs, err := s.store.EventsSince(ctx, reviewID, cursor, excludeClaude)
	if err != nil {
		return cursor
	}
	wrote := false
	for _, e := range evs {
		if e.Seq > cursor {
			cursor = e.Seq
		}
		if named && e.Type == store.EventChannelChanged {
			continue
		}
		// No `event:` field: native EventSource delivers only default-type frames
		// to onmessage, which is how the browser consumes the stream. The frame's
		// type lives inside the JSON payload instead.
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, e.Payload)
		wrote = true
	}
	if wrote {
		fl.Flush()
	}
	return cursor
}

// emitChannelChanged persists the connected flag stamped with the review's
// current version, like every other event on the log.
func (s *Server) emitChannelChanged(ctx context.Context, reviewID string, connected bool) {
	v, ok, err := s.store.LatestVersion(ctx, reviewID)
	if err != nil || !ok {
		return
	}
	s.emit(ctx, reviewID, store.OriginSystem, store.EventChannelChanged, v.VersionNumber, map[string]any{"connected": connected})
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
