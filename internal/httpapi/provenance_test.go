package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/yasyf/cc-review/internal/store"
)

const sliceLines = `{"schema":"cc-transcript.slice/1","event_uuid":"e-1","tool_use_id":"tu-1","ts_ms":1000,"tool_name":"Edit","tool_digest":"d1","file_path":"a.go","summary":"Edit a.go"}
{"schema":"cc-transcript.slice/1","event_uuid":"e-2","tool_use_id":"tu-2","ts_ms":2000,"tool_name":"Bash","tool_digest":"d2","file_path":"","summary":"go test ./..."}
`

// fakeSlice installs an executable cc-transcript stub at the front of PATH;
// each invocation appends one byte to a counter file and prints output.
func fakeSlice(t *testing.T, output string) (countPath string) {
	t.Helper()
	dir := t.TempDir()
	countPath = filepath.Join(dir, "count")
	script := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\ncat <<'EOF'\n%sEOF\n", countPath, output)
	if err := os.WriteFile(filepath.Join(dir, "cc-transcript"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return countPath
}

func sliceCalls(t *testing.T, countPath string) int {
	t.Helper()
	b, err := os.ReadFile(countPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

func getProvenance(t *testing.T, srv *httptest.Server, turnID int64) (provenanceResponse, int) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/turns/" + strconv.FormatInt(turnID, 10) + "/provenance")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return provenanceResponse{}, resp.StatusCode
	}
	var out provenanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out, resp.StatusCode
}

func createClosedTurn(t *testing.T, st *store.Store) store.Turn {
	t.Helper()
	ctx := context.Background()
	turn, err := st.CreateTurn(ctx, store.Turn{
		RepoRoot: "/repo", Backend: "git", SessionID: "sess-prov", ClaudePID: 100, TreeStart: "t0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CloseTurn(ctx, turn.ID, "t1", "closed"); err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestTurnProvenanceReturnsSliceItems(t *testing.T) {
	st, _, srv := newTestServer(t)
	turn := createClosedTurn(t, st)
	fakeSlice(t, sliceLines)

	out, status := getProvenance(t, srv, turn.ID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	want := provenanceResponse{Provenance: []provenanceItem{
		{EventUUID: "e-1", TsMs: 1000, ToolName: "Edit", Summary: "Edit a.go", FilePath: "a.go"},
		{EventUUID: "e-2", TsMs: 2000, ToolName: "Bash", Summary: "go test ./...", FilePath: ""},
	}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("provenance = %+v, want %+v", out, want)
	}
}

func TestTurnProvenanceCachesClosedTurns(t *testing.T) {
	st, _, srv := newTestServer(t)
	turn := createClosedTurn(t, st)
	count := fakeSlice(t, sliceLines)

	for range 2 {
		if out, _ := getProvenance(t, srv, turn.ID); len(out.Provenance) != 2 {
			t.Fatalf("provenance = %+v, want 2 items", out.Provenance)
		}
	}
	if calls := sliceCalls(t, count); calls != 1 {
		t.Fatalf("slice invoked %d times for a closed turn, want 1", calls)
	}
}

func TestTurnProvenanceDoesNotCacheOpenTurns(t *testing.T) {
	st, _, srv := newTestServer(t)
	turn, err := st.CreateTurn(context.Background(), store.Turn{
		RepoRoot: "/repo", Backend: "git", SessionID: "sess-prov", ClaudePID: 100, TreeStart: "t0",
	})
	if err != nil {
		t.Fatal(err)
	}
	count := fakeSlice(t, sliceLines)

	for range 2 {
		getProvenance(t, srv, turn.ID)
	}
	if calls := sliceCalls(t, count); calls != 2 {
		t.Fatalf("slice invoked %d times for an open turn, want 2 (window still moving)", calls)
	}
}

func TestTurnProvenanceDegradesWhenSliceUnavailable(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T)
	}{
		{name: "binary absent", setup: func(t *testing.T) { t.Setenv("PATH", t.TempDir()) }},
		{name: "exit 1 transcript missing", setup: func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "cc-transcript"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
		}},
		{name: "schema skew", setup: func(t *testing.T) {
			fakeSlice(t, `{"schema":"cc-transcript.slice/9","event_uuid":"e","ts_ms":1,"tool_name":"Bash","summary":"s","file_path":""}`+"\n")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, _, srv := newTestServer(t)
			turn := createClosedTurn(t, st)
			tt.setup(t)

			resp, err := http.Get(srv.URL + "/api/turns/" + strconv.FormatInt(turn.ID, 10) + "/provenance")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var out provenanceResponse
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			if !out.Unavailable || len(out.Provenance) != 0 {
				t.Fatalf("degraded response = %+v, want unavailable with no items", out)
			}
			if !bytes.Contains(body, []byte(`"provenance":[]`)) {
				t.Fatalf("degraded body = %s, want an empty provenance array, never null", body)
			}
		})
	}
}

func TestTurnProvenanceUnknownTurnIs404(t *testing.T) {
	_, _, srv := newTestServer(t)
	fakeSlice(t, sliceLines)

	if _, status := getProvenance(t, srv, 999); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}
