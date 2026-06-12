// Package httpapi is the daemon's data/UI plane: a 127.0.0.1 HTTP server that
// serves the embedded SPA, a small JSON REST surface, and one Server-Sent-Events
// stream that fans the per-review event log out to the browser and to the
// Claude-side stream consumers. It depends on the store concretely and on the
// daemon only through the narrow Backend interface, so there is no import cycle.
package httpapi

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/store"
)

// Backend is the daemon-side capability the HTTP plane needs: subscribe to a
// review's wakeup bus, append an event (which persists it and publishes the
// wakeup), register a named SSE stream consumer, and read whether a live
// Claude-side consumer is wired to a review. The daemon's appendEvent
// chokepoint, Bus, and Activity satisfy this.
type Backend interface {
	Subscribe(reviewID string) (<-chan struct{}, func())
	AppendEvent(ctx context.Context, e *store.Event) (int64, error)
	Attach(reviewID, consumer string, claudePID int) func()
	ClaudeConnected(reviewID string) bool
}

// Server is the HTTP handler tree. It is constructed by the daemon, which owns
// the listener and the chosen port. The listener binds 127.0.0.1 only, which is
// the whole access-control story.
type Server struct {
	store     *store.Store
	decisions *decisions.Log
	backend   Backend
	log       *log.Logger
	mux       *http.ServeMux

	provMu     sync.Mutex
	provCache  map[int64][]provenanceItem // closed turns only; never persisted
	provWarned map[string]bool            // session ids already warned about slice failures
}

// New builds the HTTP server.
func New(st *store.Store, ledger *decisions.Log, logger *log.Logger, backend Backend) *Server {
	s := &Server{
		store: st, decisions: ledger, backend: backend, log: logger, mux: http.NewServeMux(),
		provCache: make(map[int64][]provenanceItem), provWarned: make(map[string]bool),
	}
	s.routes()
	return s
}

// Handler returns the root handler for the daemon to serve.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/session/{reviewId}", s.handleGetSession)
	s.mux.HandleFunc("GET /api/session/{reviewId}/versions", s.handleGetVersions)
	s.mux.HandleFunc("POST /api/comments", s.handleCreateComment)
	s.mux.HandleFunc("PUT /api/comments/{id}", s.handleUpdateComment)
	s.mux.HandleFunc("POST /api/replies/{commentId}", s.handleCreateReply)
	s.mux.HandleFunc("POST /api/file-states", s.handleSetFileStates)
	s.mux.HandleFunc("POST /api/ai-requests", s.handleCreateAIRequest)
	s.mux.HandleFunc("POST /api/ai-requests/{id}/undo", s.handleUndoAIRequest)
	s.mux.HandleFunc("POST /api/submit", s.handleSubmit)
	s.mux.HandleFunc("GET /api/turns/{id}/provenance", s.handleTurnProvenance)
	s.mux.HandleFunc("GET /events", s.handleEvents)
	// Registered last and least-specific: the SPA shell + embedded assets.
	s.mux.Handle("/", s.static())
}
