package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/store"
)

func createReviewVersion(t *testing.T, st *store.Store, filesJSON string) (store.Review, store.Version) {
	t.Helper()
	ctx := context.Background()
	review, err := st.CreateReview(ctx, "s1", 100, "/repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(t.TempDir(), "p.patch")
	if err := os.WriteFile(patch, []byte("diff --git a/a.go b/a.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	version, err := st.CreateVersion(ctx, review.ID, "main", "HEAD", patch, filesJSON)
	if err != nil {
		t.Fatal(err)
	}
	return review, version
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func nextEvent(t *testing.T, backend *stubBackend) *store.Event {
	t.Helper()
	select {
	case ev := <-backend.events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event emitted")
		return nil
	}
}

func boolPtr(b bool) *bool { return &b }

func TestSetFileStatesAppliesAndEmits(t *testing.T) {
	st, backend, srv := newTestServer(t)
	review, _ := createReviewVersion(t, st,
		`[{"path":"a.go","status":"M","fingerprint":"fp-a"},{"path":"b.go","status":"A","fingerprint":"fp-b"}]`)

	resp := postJSON(t, srv.URL+"/api/file-states", map[string]any{
		"reviewId": review.ID,
		"files": []map[string]any{
			{"path": "a.go", "reviewed": true},
			{"path": "b.go", "hidden": true},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		States []fileStateOut `json:"states"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	want := []fileStateOut{{Path: "a.go", Reviewed: true}, {Path: "b.go", Hidden: true}}
	if !reflect.DeepEqual(out.States, want) {
		t.Fatalf("states = %+v, want %+v", out.States, want)
	}

	states, err := st.ListFileStates(context.Background(), review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("got %d file-state rows, want 2", len(states))
	}
	a, b := states[0], states[1]
	if a.Path != "a.go" || !a.Reviewed || a.Hidden || a.ReviewedFingerprint != "fp-a" {
		t.Fatalf("a.go state = %+v, want reviewed with fingerprint fp-a", a)
	}
	if b.Path != "b.go" || b.Reviewed || !b.Hidden || b.ReviewedFingerprint != "" {
		t.Fatalf("b.go state = %+v, want hidden and unreviewed", b)
	}

	ev := nextEvent(t, backend)
	if ev.Type != store.EventFileStates || ev.Origin != store.OriginUser || ev.VersionNumber != 1 {
		t.Fatalf("event type=%s origin=%s version=%d, want user file.states on version 1",
			ev.Type, ev.Origin, ev.VersionNumber)
	}
	var payload struct {
		States []struct {
			Path     string `json:"path"`
			Reviewed bool   `json:"reviewed"`
			Hidden   bool   `json:"hidden"`
		} `json:"states"`
	}
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.States) != 2 ||
		payload.States[0].Path != "a.go" || !payload.States[0].Reviewed ||
		payload.States[1].Path != "b.go" || !payload.States[1].Hidden {
		t.Fatalf("event states = %+v, want a.go reviewed and b.go hidden", payload.States)
	}
}

func TestCreateAIRequestEmptyPromptIs400(t *testing.T) {
	st, backend, srv := newTestServer(t)
	review, _ := createReviewVersion(t, st, `[]`)

	resp := postJSON(t, srv.URL+"/api/ai-requests", map[string]any{"reviewId": review.ID, "prompt": "   "})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if requests, _ := st.ListAIRequests(context.Background(), review.ID); len(requests) != 0 {
		t.Fatalf("got %d persisted requests, want 0", len(requests))
	}
	select {
	case ev := <-backend.events:
		t.Fatalf("event %s emitted for a rejected request", ev.Type)
	default:
	}
}

func TestUndoAIRequestNotDoneIs409(t *testing.T) {
	for _, status := range []string{"pending", "working"} {
		t.Run(status, func(t *testing.T) {
			st, backend, srv := newTestServer(t)
			ctx := context.Background()
			review, version := createReviewVersion(t, st, `[{"path":"a.go","status":"M","fingerprint":"fp-a"}]`)
			ar, err := st.CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, "mark a.go")
			if err != nil {
				t.Fatal(err)
			}
			if status != "pending" {
				if _, err := st.TransitionAIRequest(ctx, ar.ID, status, "", nil); err != nil {
					t.Fatal(err)
				}
			}
			results, err := st.ApplyFileStates(ctx, review.ID,
				[]store.FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}}, map[string]string{"a.go": "fp-a"})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AppendAIRequestChanges(ctx, ar.ID,
				[]store.AIChange{{Path: "a.go", Prior: results[0].Prior, Applied: results[0].Applied}}); err != nil {
				t.Fatal(err)
			}

			resp, err := http.Post(srv.URL+"/api/ai-requests/"+strconv.FormatInt(ar.ID, 10)+"/undo", "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409", resp.StatusCode)
			}
			// The 409 path must be side-effect free: status kept, the in-flight
			// batch's states untouched, nothing emitted.
			got, err := st.GetAIRequest(ctx, ar.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != status {
				t.Fatalf("status = %q after refused undo, want %q", got.Status, status)
			}
			states, _ := st.ListFileStates(ctx, review.ID)
			if len(states) != 1 || !states[0].Reviewed {
				t.Fatalf("file states = %+v, want a.go still reviewed", states)
			}
			select {
			case ev := <-backend.events:
				t.Fatalf("event %s emitted for a refused undo", ev.Type)
			default:
			}
		})
	}
}

func TestUndoAIRequestRestoresStatesThenUpdatesRequest(t *testing.T) {
	st, backend, srv := newTestServer(t)
	ctx := context.Background()
	review, version := createReviewVersion(t, st, `[{"path":"a.go","status":"M","fingerprint":"fp-a"}]`)
	ar, err := st.CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, "mark a.go")
	if err != nil {
		t.Fatal(err)
	}
	results, err := st.ApplyFileStates(ctx, review.ID,
		[]store.FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}}, map[string]string{"a.go": "fp-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAIRequestChanges(ctx, ar.ID,
		[]store.AIChange{{Path: "a.go", Prior: results[0].Prior, Applied: results[0].Applied}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionAIRequest(ctx, ar.ID, "done", "marked a.go", nil); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/ai-requests/"+strconv.FormatInt(ar.ID, 10)+"/undo", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	states, err := st.ListFileStates(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Reviewed || states[0].Hidden || states[0].ReviewedFingerprint != "" {
		t.Fatalf("file states = %+v, want a.go restored to unreviewed", states)
	}
	got, err := st.GetAIRequest(ctx, ar.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "undone" {
		t.Fatalf("status = %q, want undone", got.Status)
	}

	first := nextEvent(t, backend)
	if first.Type != store.EventFileStates || first.Origin != store.OriginUser {
		t.Fatalf("first event type=%s origin=%s, want user file.states before ai.request.updated",
			first.Type, first.Origin)
	}
	var restore struct {
		UndoOf string `json:"undoOf"`
		States []struct {
			Path     string `json:"path"`
			Reviewed bool   `json:"reviewed"`
			Hidden   bool   `json:"hidden"`
		} `json:"states"`
	}
	if err := json.Unmarshal(first.Payload, &restore); err != nil {
		t.Fatal(err)
	}
	if restore.UndoOf != strconv.FormatInt(ar.ID, 10) {
		t.Fatalf("undoOf = %q, want %d", restore.UndoOf, ar.ID)
	}
	if len(restore.States) != 1 || restore.States[0].Path != "a.go" || restore.States[0].Reviewed || restore.States[0].Hidden {
		t.Fatalf("restored states = %+v, want a.go unreviewed", restore.States)
	}

	second := nextEvent(t, backend)
	if second.Type != store.EventAIRequestUpdated {
		t.Fatalf("second event type = %s, want ai.request.updated", second.Type)
	}
	var updated struct {
		Request struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"request"`
	}
	if err := json.Unmarshal(second.Payload, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Request.ID != strconv.FormatInt(ar.ID, 10) || updated.Request.Status != "undone" {
		t.Fatalf("updated request = %+v, want id %d undone", updated.Request, ar.ID)
	}
}

func TestSessionCarriesLatestEventSeq(t *testing.T) {
	st, _, srv := newTestServer(t)
	ctx := context.Background()
	review, _ := createReviewVersion(t, st, `[]`)

	if got := getSessionLatestSeq(t, srv, review.ID); got != "0" {
		t.Fatalf("latestEventSeq = %q before any events, want \"0\"", got)
	}
	for range 2 {
		if _, err := st.AppendEvent(ctx, &store.Event{ReviewID: review.ID, Origin: store.OriginUser, Type: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := getSessionLatestSeq(t, srv, review.ID); got != "2" {
		t.Fatalf("latestEventSeq = %q after two events, want \"2\"", got)
	}
}

func getSessionLatestSeq(t *testing.T, srv *httptest.Server, ref string) string {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/session/" + ref)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		LatestEventSeq string `json:"latestEventSeq"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.LatestEventSeq
}
