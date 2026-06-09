// Package httpapi is the daemon's data/UI plane: a 127.0.0.1 HTTP server that
// serves the embedded SPA, a small JSON REST surface, and one Server-Sent-Events
// stream that fans the per-review event log out to the browser and to the
// Claude-side stream consumers. It depends on the store concretely and on the
// daemon only through the narrow Backend interface, so there is no import cycle.
package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/yasyf/cc-review/internal/store"
)

// Backend is the daemon-side capability the HTTP plane needs: subscribe to a
// review's wakeup bus, and append an event (which persists it and publishes the
// wakeup). The daemon's appendEvent chokepoint and Bus satisfy this.
type Backend interface {
	Subscribe(reviewID string) (<-chan struct{}, func())
	AppendEvent(ctx context.Context, e *store.Event) (int64, error)
}

// Server is the HTTP handler tree. It is constructed by the daemon, which owns
// the listener and the chosen port.
type Server struct {
	store   *store.Store
	backend Backend
	token   string
	mux     *http.ServeMux
}

// New builds the HTTP server. token gates every /api and /events request.
func New(st *store.Store, backend Backend, token string) *Server {
	s := &Server{store: st, backend: backend, token: token, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the root handler for the daemon to serve.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/session/{reviewId}", s.withToken(s.handleGetSession))
	s.mux.HandleFunc("GET /api/session/{reviewId}/versions", s.withToken(s.handleGetVersions))
	s.mux.HandleFunc("POST /api/comments", s.withToken(s.handleCreateComment))
	s.mux.HandleFunc("PUT /api/comments/{id}", s.withToken(s.handleUpdateComment))
	s.mux.HandleFunc("POST /api/replies/{commentId}", s.withToken(s.handleCreateReply))
	s.mux.HandleFunc("POST /api/submit", s.withToken(s.handleSubmit))
	s.mux.HandleFunc("GET /events", s.withToken(s.handleEvents))
	// Registered last and least-specific: the SPA shell + embedded assets.
	s.mux.Handle("/", s.static())
}

// withToken gates a handler on the ?t= access token, compared in constant time.
func (s *Server) withToken(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("t")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}
