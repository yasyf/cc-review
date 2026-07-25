package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	ccevent "github.com/yasyf/cc-interact/event"
	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/web"
	"github.com/yasyf/cc-review/internal/wire"
)

func newTestServer(t *testing.T) (*store.Store, *ccstore.Store, *httptest.Server) {
	st, _, cc, srv := newTestServerWithLedger(t)
	return st, cc, srv
}

func newTestServerWithLedger(t *testing.T) (*store.Store, *decisions.Log, *ccstore.Store, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	cc, err := ccstore.Open(t.Context(), filepath.Join(dir, "t.db"), store.Schema())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	ledger, err := decisions.Open(t.Context(), filepath.Join(dir, "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	st := store.New(cc.DB())
	mux := http.NewServeMux()
	RESTMount(mux, Deps{
		DB: cc.DB, Decisions: ledger, Log: log.New(io.Discard, "", 0),
		Append: cc.AppendEvent, ConsumerConnected: func(string) bool { return false }, Dist: web.Dist(),
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, ledger, cc, srv
}

// eventLog reads the event log a handler wrote, in seq order, advancing a
// cursor — the test-side replacement for the old stub backend's event channel
// now that mutations append to the real cc-interact event store.
type eventLog struct {
	t   *testing.T
	cc  *ccstore.Store
	id  string
	seq int64
}

func newEventLog(t *testing.T, cc *ccstore.Store, reviewID string) *eventLog {
	return &eventLog{t: t, cc: cc, id: reviewID}
}

func (e *eventLog) next() ccevent.Event {
	e.t.Helper()
	evs, err := e.cc.EventsSince(context.Background(), e.id, e.seq, "")
	if err != nil {
		e.t.Fatal(err)
	}
	if len(evs) == 0 {
		e.t.Fatal("no event emitted")
	}
	e.seq = evs[0].Seq
	return evs[0]
}

func (e *eventLog) none() {
	e.t.Helper()
	evs, err := e.cc.EventsSince(context.Background(), e.id, e.seq, "")
	if err != nil {
		e.t.Fatal(err)
	}
	if len(evs) > 0 {
		e.t.Fatalf("event %s emitted, want none", evs[0].Type)
	}
}

func createReviewVersion(t *testing.T, st *store.Store, filesJSON string) (store.Review, store.Version, store.Section) {
	t.Helper()
	ctx := context.Background()
	ss := ccstore.NewSubjectStore(st.DB())
	sub, err := ss.Create(ctx, store.NewSlugHash(), store.ReviewSlug(store.NewSlugHash()), "s1", "/repo", 100, "open")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetReviewMeta(ctx, sub.ID, "base0", "main", false); err != nil {
		t.Fatal(err)
	}
	version, sections, err := st.CreateVersion(ctx, sub.ID, "main", "HEAD", "",
		[]store.SectionInput{{Position: 0, Branch: "main", BaseRef: "HEAD", Pending: true, FilesJSON: filesJSON}})
	if err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(t.TempDir(), "p.patch")
	if err := os.WriteFile(patch, []byte("diff --git a/a.go b/a.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSectionPatchPath(ctx, sections[0].ID, patch); err != nil {
		t.Fatal(err)
	}
	sections[0].PatchPath = patch
	review, err := st.GetReview(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	return review, version, sections[0]
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b)) //nolint:gosec // G107: url is the test's own httptest server address, not external input.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func boolPtr(b bool) *bool { return &b }

func TestSetFileStatesAppliesAndEmits(t *testing.T) {
	st, cc, srv := newTestServer(t)
	review, _, _ := createReviewVersion(t, st,
		`[{"path":"a.go","status":"M","fingerprint":"fp-a","generated":false,"vendored":false},{"path":"b.go","status":"A","fingerprint":"fp-b","generated":false,"vendored":false}]`)

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

	ev := newEventLog(t, cc, review.ID).next()
	if ev.Type != store.EventFileStates || ev.Origin != ccevent.OriginHuman {
		t.Fatalf("event type=%s origin=%s, want human file.states", ev.Type, ev.Origin)
	}
	var payload struct {
		VersionNumber int `json:"version_number"`
		States        []struct {
			Path     string `json:"path"`
			Reviewed bool   `json:"reviewed"`
			Hidden   bool   `json:"hidden"`
		} `json:"states"`
	}
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.VersionNumber != 1 {
		t.Fatalf("event version_number = %d, want 1", payload.VersionNumber)
	}
	if len(payload.States) != 2 ||
		payload.States[0].Path != "a.go" || !payload.States[0].Reviewed ||
		payload.States[1].Path != "b.go" || !payload.States[1].Hidden {
		t.Fatalf("event states = %+v, want a.go reviewed and b.go hidden", payload.States)
	}
}

func TestCreateAIRequestEmptyPromptIs400(t *testing.T) {
	st, cc, srv := newTestServer(t)
	review, _, _ := createReviewVersion(t, st, `[]`)

	resp := postJSON(t, srv.URL+"/api/ai-requests", map[string]any{"reviewId": review.ID, "prompt": "   "})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if requests, _ := st.ListAIRequests(context.Background(), review.ID); len(requests) != 0 {
		t.Fatalf("got %d persisted requests, want 0", len(requests))
	}
	newEventLog(t, cc, review.ID).none()
}

func TestAnswerAIRequest(t *testing.T) {
	t.Run("awaiting_input is answered and re-dispatched", func(t *testing.T) {
		st, cc, srv := newTestServer(t)
		ctx := context.Background()
		review, version, _ := createReviewVersion(t, st, `[{"path":"a.go","status":"M","fingerprint":"fp-a","generated":false,"vendored":false}]`)
		ar, err := st.CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, "mark the boring ones")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.TransitionAIRequest(ctx, ar.ID, "working", "", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AskAIRequest(ctx, ar.ID, store.AIQuestion{Body: "which files?"}); err != nil {
			t.Fatal(err)
		}

		resp := postJSON(t, srv.URL+"/api/ai-requests/"+strconv.FormatInt(ar.ID, 10)+"/answer",
			map[string]any{"answer": "generated only"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got, err := st.GetAIRequest(ctx, ar.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "answered" || got.Attempt != 1 || got.Answer == nil || got.Answer.Text != "generated only" {
			t.Fatalf("got %+v, want answered/attempt=1/answer=generated only", got)
		}
		// Emitted as ai.request.created so the skill redispatches a fresh run.
		if ev := newEventLog(t, cc, review.ID).next(); ev.Type != store.EventAIRequestCreated {
			t.Fatalf("event type = %q, want %q", ev.Type, store.EventAIRequestCreated)
		}
	})

	t.Run("non-awaiting request is 409", func(t *testing.T) {
		st, _, srv := newTestServer(t)
		ctx := context.Background()
		review, version, _ := createReviewVersion(t, st, `[]`)
		ar, err := st.CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, "x")
		if err != nil {
			t.Fatal(err)
		}
		resp := postJSON(t, srv.URL+"/api/ai-requests/"+strconv.FormatInt(ar.ID, 10)+"/answer",
			map[string]any{"answer": "y"})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409", resp.StatusCode)
		}
	})

	t.Run("stale-version question is 409", func(t *testing.T) {
		st, _, srv := newTestServer(t)
		ctx := context.Background()
		review, version, _ := createReviewVersion(t, st, `[{"path":"a.go","status":"M","fingerprint":"fp-a","generated":false,"vendored":false}]`)
		ar, err := st.CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, "q")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.TransitionAIRequest(ctx, ar.ID, "working", "", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AskAIRequest(ctx, ar.ID, store.AIQuestion{Body: "which?"}); err != nil {
			t.Fatal(err)
		}
		// A newer version supersedes the question's version.
		if _, _, err := st.CreateVersion(ctx, review.ID, "main", "HEAD", "",
			[]store.SectionInput{{Position: 0, Branch: "main", BaseRef: "HEAD", Pending: true, FilesJSON: `[{"path":"a.go","status":"M","fingerprint":"fp-a2","generated":false,"vendored":false}]`}}); err != nil {
			t.Fatal(err)
		}
		resp := postJSON(t, srv.URL+"/api/ai-requests/"+strconv.FormatInt(ar.ID, 10)+"/answer",
			map[string]any{"answer": "y"})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409", resp.StatusCode)
		}
	})
}

func TestUndoAIRequestNotDoneIs409(t *testing.T) {
	for _, status := range []string{"pending", "working"} {
		t.Run(status, func(t *testing.T) {
			st, cc, srv := newTestServer(t)
			ctx := context.Background()
			review, version, _ := createReviewVersion(t, st, `[{"path":"a.go","status":"M","fingerprint":"fp-a","generated":false,"vendored":false}]`)
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
				[]store.FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}}, map[store.SectionFileKey]string{{Path: "a.go"}: "fp-a"})
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
			defer func() { _ = resp.Body.Close() }()
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
			newEventLog(t, cc, review.ID).none()
		})
	}
}

func TestUndoAIRequestRestoresStatesThenUpdatesRequest(t *testing.T) {
	st, cc, srv := newTestServer(t)
	ctx := context.Background()
	review, version, _ := createReviewVersion(t, st, `[{"path":"a.go","status":"M","fingerprint":"fp-a","generated":false,"vendored":false}]`)
	ar, err := st.CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, "mark a.go")
	if err != nil {
		t.Fatal(err)
	}
	results, err := st.ApplyFileStates(ctx, review.ID,
		[]store.FileStateInput{{Path: "a.go", Reviewed: boolPtr(true)}}, map[store.SectionFileKey]string{{Path: "a.go"}: "fp-a"})
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
	defer func() { _ = resp.Body.Close() }()
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

	events := newEventLog(t, cc, review.ID)
	first := events.next()
	if first.Type != store.EventFileStates || first.Origin != ccevent.OriginHuman {
		t.Fatalf("first event type=%s origin=%s, want human file.states before ai.request.updated",
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

	second := events.next()
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

func TestCloseReviewDetachesAndEmits(t *testing.T) {
	st, cc, srv := newTestServer(t)
	ctx := context.Background()
	review, version, _ := createReviewVersion(t, st, `[]`)

	resp := postJSON(t, srv.URL+"/api/close", map[string]any{"reviewId": review.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("body ok = false, want true")
	}

	sub, err := ccstore.NewSubjectStore(st.DB()).Get(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != "closed" {
		t.Fatalf("subject status = %q, want closed", sub.Status)
	}
	if sub.SessionID != "" || sub.ClaudePID != 0 {
		t.Fatalf("subject session=%q pid=%d, want detached (empty session, pid 0)", sub.SessionID, sub.ClaudePID)
	}

	events := newEventLog(t, cc, review.ID)
	ev := events.next()
	if ev.Type != store.EventStatusChanged || ev.Origin != ccevent.OriginHuman {
		t.Fatalf("event type=%s origin=%s, want human status.changed", ev.Type, ev.Origin)
	}
	var payload struct {
		VersionNumber int    `json:"version_number"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "closed" || payload.VersionNumber != version.VersionNumber {
		t.Fatalf("payload = %+v, want status closed at version %d", payload, version.VersionNumber)
	}
	events.none()
}

func TestCloseSubmittedReviewIs409(t *testing.T) {
	st, cc, srv := newTestServer(t)
	ctx := context.Background()
	review, _, _ := createReviewVersion(t, st, `[]`)
	ss := ccstore.NewSubjectStore(st.DB())
	if err := ss.SetStatus(ctx, review.ID, "submitted"); err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv.URL+"/api/close", map[string]any{"reviewId": review.ID})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	sub, err := ss.Get(ctx, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != "submitted" {
		t.Fatalf("subject status = %q after refused close, want submitted", sub.Status)
	}
	if sub.SessionID != "s1" || sub.ClaudePID != 100 {
		t.Fatalf("subject session=%q pid=%d after refused close, want still attached (s1, 100)", sub.SessionID, sub.ClaudePID)
	}
	newEventLog(t, cc, review.ID).none()
}

func TestSessionCarriesLatestEventSeq(t *testing.T) {
	st, cc, srv := newTestServer(t)
	ctx := context.Background()
	review, _, _ := createReviewVersion(t, st, `[]`)

	if got := getSessionLatestSeq(t, srv, review.ID); got != "0" {
		t.Fatalf("latestEventSeq = %q before any events, want \"0\"", got)
	}
	for range 2 {
		if _, err := cc.AppendEvent(ctx, &ccevent.Event{SubjectID: review.ID, Origin: ccevent.OriginHuman, Type: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := getSessionLatestSeq(t, srv, review.ID); got != "2" {
		t.Fatalf("latestEventSeq = %q after two events, want \"2\"", got)
	}
}

func getSessionLatestSeq(t *testing.T, srv *httptest.Server, ref string) string {
	t.Helper()
	var out struct {
		LatestEventSeq string `json:"latestEventSeq"`
	}
	if err := json.Unmarshal(getSessionBody(t, srv, ref), &out); err != nil {
		t.Fatal(err)
	}
	return out.LatestEventSeq
}

func getSessionBody(t *testing.T, srv *httptest.Server, ref string) []byte {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/session/" + ref)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSessionFilesPassThroughGeneratedFlag(t *testing.T) {
	st, _, srv := newTestServer(t)
	review, _, _ := createReviewVersion(t, st,
		`[{"path":"package-lock.json","status":"A","fingerprint":"fp-a","generated":true,"vendored":false},`+
			`{"path":"main.go","status":"A","fingerprint":"fp-b","generated":false,"vendored":false}]`)

	var session struct {
		Sections []struct {
			Files json.RawMessage `json:"files"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(getSessionBody(t, srv, review.ID), &session); err != nil {
		t.Fatal(err)
	}
	if len(session.Sections) != 1 || !bytes.Contains(session.Sections[0].Files, []byte(`"generated":true`)) {
		t.Fatalf("session files JSON dropped the generated flag (raw passthrough broken): %+v", session.Sections)
	}
}

func TestSessionCarriesTurnsAndAttributions(t *testing.T) {
	st, _, srv := newTestServer(t)
	ctx := context.Background()
	review, _, section := createReviewVersion(t, st, `[]`)
	turns := vcs.NewTurnStore(st.DB())

	t1, err := turns.CreateTurn(ctx, vcs.Turn{RepoRoot: "/repo", Backend: "git", SessionID: "s1", ClaudePID: 100, PromptExcerpt: "add parser"})
	if err != nil {
		t.Fatal(err)
	}
	if err := turns.CloseTurn(ctx, t1.ID, "tree1", "closed"); err != nil {
		t.Fatal(err)
	}
	t2, err := turns.CreateTurn(ctx, vcs.Turn{RepoRoot: "/repo", Backend: "git", SessionID: "s1", ClaudePID: 100, PromptExcerpt: "fix tests"})
	if err != nil {
		t.Fatal(err)
	}
	if err := turns.CloseOpenTurnsForWindow(ctx, "/repo", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.PutAttributions(ctx, section.ID, map[string][]store.AttributionRange{
		"a.go": {{Start: 1, End: 3, TurnID: t2.ID}, {Start: 7, End: 9}},
		"b.go": {{Start: 2, End: 2, TurnID: t1.ID}},
	}); err != nil {
		t.Fatal(err)
	}

	body := getSessionBody(t, srv, review.ID)
	var out struct {
		Turns    []wire.Turn `json:"turns"`
		Sections []struct {
			Attributions map[string][]wire.AttributionRange `json:"attributions"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}

	stored, err := turns.ListTurnsByIDs(ctx, []int64{t1.ID, t2.ID})
	if err != nil {
		t.Fatal(err)
	}
	wantTurns := []wire.Turn{
		{
			ID: strconv.FormatInt(t1.ID, 10), SessionID: "s1", PromptExcerpt: "add parser",
			StartedAt: stored[0].StartedAt, EndedAt: stored[0].EndedAt,
		},
		{
			ID: strconv.FormatInt(t2.ID, 10), SessionID: "s1", PromptExcerpt: "fix tests", Interrupted: true,
			StartedAt: stored[1].StartedAt, EndedAt: stored[1].EndedAt,
		},
	}
	if !reflect.DeepEqual(out.Turns, wantTurns) {
		t.Fatalf("turns = %+v, want %+v", out.Turns, wantTurns)
	}
	wantAttrs := map[string][]wire.AttributionRange{
		"a.go": {{Start: 1, End: 3, TurnID: strconv.FormatInt(t2.ID, 10)}, {Start: 7, End: 9}},
		"b.go": {{Start: 2, End: 2, TurnID: strconv.FormatInt(t1.ID, 10)}},
	}
	if len(out.Sections) != 1 || !reflect.DeepEqual(out.Sections[0].Attributions, wantAttrs) {
		t.Fatalf("attributions = %+v, want %+v", out.Sections, wantAttrs)
	}

	var raw struct {
		Sections []struct {
			Attributions map[string][]map[string]json.RawMessage `json:"attributions"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Sections[0].Attributions["a.go"][1]["turnId"]; ok {
		t.Fatalf("untagged range serialized a turnId key: %s", body)
	}
}

func TestSessionEmptyTurnsAndAttributionsSerializeNonNull(t *testing.T) {
	st, _, srv := newTestServer(t)
	review, _, _ := createReviewVersion(t, st, `[]`)

	body := getSessionBody(t, srv, review.ID)
	if !bytes.Contains(body, []byte(`"turns":[]`)) {
		t.Fatalf(`session body lacks "turns":[]: %s`, body)
	}
	if !bytes.Contains(body, []byte(`"attributions":{}`)) {
		t.Fatalf(`session body lacks "attributions":{}: %s`, body)
	}
	if !bytes.Contains(body, []byte(`"turnActivity":{}`)) {
		t.Fatalf(`session body lacks "turnActivity":{}: %s`, body)
	}
}

func TestSessionCarriesTurnActivity(t *testing.T) {
	st, ledger, _, srv := newTestServerWithLedger(t)
	ctx := context.Background()
	review, _, section := createReviewVersion(t, st, `[]`)

	turn, err := vcs.NewTurnStore(st.DB()).CreateTurn(ctx, vcs.Turn{RepoRoot: "/repo", Backend: "git", SessionID: "s1", ClaudePID: 100, PromptExcerpt: "add parser"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAttributions(ctx, section.ID, map[string][]store.AttributionRange{
		"a.go": {{Start: 1, End: 3, TurnID: turn.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	inWindow := decisions.Decision{
		TsMs: turn.StartedAt + 1, SessionID: "s1", Source: "cc-review", Kind: "gate",
		Event: "PreToolUse", Action: "block", ToolName: "Edit", Message: "locked review",
	}
	beforeWindow := inWindow
	beforeWindow.TsMs = turn.StartedAt - 10
	otherSession := inWindow
	otherSession.SessionID = "s2"
	for _, d := range []decisions.Decision{inWindow, beforeWindow, otherSession} {
		if err := ledger.Append(d); err != nil {
			t.Fatal(err)
		}
	}

	var out struct {
		TurnActivity map[string][]wire.Decision `json:"turnActivity"`
	}
	if err := json.Unmarshal(getSessionBody(t, srv, review.ID), &out); err != nil {
		t.Fatal(err)
	}
	want := map[string][]wire.Decision{
		strconv.FormatInt(turn.ID, 10): {{
			TsMs: turn.StartedAt + 1, Source: "cc-review", Kind: "gate",
			Action: "block", ToolName: "Edit", Message: "locked review",
		}},
	}
	if !reflect.DeepEqual(out.TurnActivity, want) {
		t.Fatalf("turnActivity = %+v, want %+v", out.TurnActivity, want)
	}
}
