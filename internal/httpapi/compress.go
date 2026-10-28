package httpapi

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// gzipped compresses h's responses for clients that accept gzip. Range is
// dropped from compressed requests, since a byte range of the identity body
// means nothing once the body is gzip.
func gzipped(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.ServeHTTP(w, r)
			return
		}
		r = r.Clone(r.Context())
		r.Header.Del("Range")
		gw := &gzipWriter{ResponseWriter: w, head: r.Method == http.MethodHead}
		h.ServeHTTP(gw, r)
		if gw.gz != nil {
			_ = gw.gz.Close()
		}
	})
}

type gzipWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	head, wrote bool
}

func (g *gzipWriter) WriteHeader(status int) {
	if g.wrote {
		return
	}
	g.wrote = true
	if !g.head && status >= http.StatusOK && status != http.StatusNoContent && status != http.StatusNotModified {
		g.Header().Del("Content-Length")
		g.Header().Set("Content-Encoding", "gzip")
		g.gz = gzip.NewWriter(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.wrote {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz == nil {
		return g.ResponseWriter.Write(b)
	}
	return g.gz.Write(b)
}
