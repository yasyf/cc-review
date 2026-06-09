package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/yasyf/cc-review/internal/web"
)

// static serves the embedded SPA: real files (hashed assets, index.html) are
// served directly with their correct Content-Type via http.FileServerFS; any
// other path is a client-side route (e.g. /s/{reviewId}) and falls back to
// index.html so deep links load the app. Registered last so it never shadows the
// /api and /events routes.
func (s *Server) static() http.Handler {
	dist := web.Dist()
	fileServer := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := dist.Open(p); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		serveIndex(w, dist)
	})
}

func serveIndex(w http.ResponseWriter, dist fs.FS) {
	b, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "spa shell missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
