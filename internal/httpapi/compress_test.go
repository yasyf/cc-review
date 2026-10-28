package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func getGzip(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("GET %s: Content-Encoding = %q, want gzip", url, resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestStaticAssetsAreGzippedAndImmutable(t *testing.T) {
	script := strings.Repeat("console.log('cc-review');\n", 4096)
	public := http.NewServeMux()
	RESTMount(http.NewServeMux(), public, Deps{Dist: fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html><title>cc-review</title>")},
		"assets/index-abc.js": {Data: []byte(script)},
	}})
	srv := httptest.NewServer(public)
	t.Cleanup(srv.Close)

	resp, body := getGzip(t, srv.URL+"/assets/index-abc.js")
	if string(body) != script {
		t.Fatalf("gunzipped asset differs from the file: %d bytes, want %d", len(body), len(script))
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control = %q", got)
	}

	resp, body = getGzip(t, srv.URL+"/s/abc123")
	if !bytes.Contains(body, []byte("<title>cc-review</title>")) || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("deep link = %q as %q, want the SPA shell as text/html", body, resp.Header.Get("Content-Type"))
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("shell Cache-Control = %q, want no-cache", got)
	}
}

func TestSessionIsGzippedWhenAccepted(t *testing.T) {
	st, _, srv := newTestServer(t)
	review, _, _ := createReviewVersion(t, st, `[]`)

	_, body := getGzip(t, srv.URL+"/api/session/"+review.ID)
	var out struct {
		LatestEventSeq string `json:"latestEventSeq"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.LatestEventSeq != "0" {
		t.Fatalf("gunzipped session = %s, %v; want JSON with latestEventSeq 0", body, err)
	}
}
