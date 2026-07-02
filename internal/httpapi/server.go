// Package httpapi is cc-review's REST plane, mounted on the cc-interact daemon's
// mux. It serves the embedded SPA and a small JSON surface; the realtime SSE
// stream is the daemon's own /events plane. Every event it writes goes through
// the daemon's Append chokepoint so the bus wakes the stream.
package httpapi

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"sync"

	ccevent "github.com/yasyf/cc-interact/event"
	"github.com/yasyf/cc-interact/sse"
	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-interact/subject"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/store"
)

// appendFunc persists an event then publishes its subject's wakeup — the daemon's
// single persist→publish chokepoint, handed to the REST plane so its mutations
// reach the SSE stream.
type appendFunc = func(ctx context.Context, e *ccevent.Event) (int64, error)

// Deps is everything the REST plane needs from the daemon process: the domain
// store, the shared decision ledger, the logger, the Append chokepoint, the
// named-consumer presence predicate, and the embedded SPA.
type Deps struct {
	Store             *store.Store
	Decisions         *decisions.Log
	Log               *log.Logger
	Append            appendFunc
	ConsumerConnected func(reviewID string) bool
	Dist              fs.FS
}

// Server holds the REST handlers' shared state.
type Server struct {
	store     *store.Store
	subjects  subject.Store
	turns     *vcs.TurnStore
	decisions *decisions.Log
	log       *log.Logger
	append    appendFunc
	connected func(reviewID string) bool

	provMu     sync.Mutex
	provCache  map[int64][]provenanceItem // closed turns only; never persisted
	provWarned map[string]bool            // session ids already warned about slice failures
}

// RESTMount registers cc-review's REST routes and the SPA static handler on the
// daemon's mux. The daemon already mounts GET /events; Go's pattern mux gives the
// more specific /api routes precedence over the catch-all "/".
func RESTMount(mux *http.ServeMux, d Deps) {
	s := &Server{
		store:      d.Store,
		subjects:   ccstore.NewSubjectStore(d.Store.DB()),
		turns:      vcs.NewTurnStore(d.Store.DB()),
		decisions:  d.Decisions,
		log:        d.Log,
		append:     d.Append,
		connected:  d.ConsumerConnected,
		provCache:  make(map[int64][]provenanceItem),
		provWarned: make(map[string]bool),
	}
	mux.HandleFunc("GET /api/session/{reviewId}", s.handleGetSession)
	mux.HandleFunc("GET /api/session/{reviewId}/versions", s.handleGetVersions)
	mux.HandleFunc("POST /api/comments", s.handleCreateComment)
	mux.HandleFunc("PUT /api/comments/{id}", s.handleUpdateComment)
	mux.HandleFunc("POST /api/replies/{commentId}", s.handleCreateReply)
	mux.HandleFunc("POST /api/file-states", s.handleSetFileStates)
	mux.HandleFunc("POST /api/ai-requests", s.handleCreateAIRequest)
	mux.HandleFunc("POST /api/ai-requests/{id}/answer", s.handleAnswerAIRequest)
	mux.HandleFunc("POST /api/ai-requests/{id}/undo", s.handleUndoAIRequest)
	mux.HandleFunc("POST /api/submit", s.handleSubmit)
	mux.HandleFunc("POST /api/close", s.handleClose)
	mux.HandleFunc("GET /api/turns/{id}/provenance", s.handleTurnProvenance)
	// Registered last and least-specific: the SPA shell + embedded assets.
	mux.Handle("/", sse.StaticHandler(d.Dist))
}
