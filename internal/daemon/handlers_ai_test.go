package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/store"
)

func bptr(b bool) *bool { return &b }

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// startedReview boots a review for window 100 over two files and returns the
// request template stamped with that window's identity.
func startedReview(t *testing.T, s *Server, repo string) (Request, Response) {
	t.Helper()
	s.alive = aliveSet(100)
	writeFile(t, repo, "a.go", "package a\n")
	writeFile(t, repo, "b.go", "package b\n")
	req := Request{Session: "sA", ClaudePID: 100, Cwd: repo}
	started := s.handleStart(context.Background(), req)
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	return req, started
}

func eventsOfType(t *testing.T, s *Server, reviewID, typ string, excludeClaude bool) []store.Event {
	t.Helper()
	events, err := s.store.EventsSince(context.Background(), reviewID, 0, excludeClaude)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestHandleFileStatesRejectsUnknownPaths(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)

	req.Files = []FileStateInput{
		{Path: "a.go", Reviewed: bptr(true)},
		{Path: "nope.go", Reviewed: bptr(true)},
	}
	resp := s.handleFileStates(ctx, req)
	if resp.OK {
		t.Fatal("unknown path must reject the batch")
	}
	if !strings.Contains(resp.Error, "nope.go") {
		t.Fatalf("error %q must enumerate the unknown path", resp.Error)
	}
	// Fail fast: nothing from the rejected batch landed.
	states, err := s.store.ListFileStates(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("rejected batch applied state: %+v", states)
	}
	if got := countEvents(t, s, started.ReviewID, store.EventFileStates); got != 0 {
		t.Fatalf("rejected batch emitted %d file.states events", got)
	}
}

func TestHandleFileStatesRecordsFirstPriorAcrossBatches(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)
	ar, err := s.store.CreateAIRequest(ctx, started.ReviewID, started.Version, "user", "mark a.go")
	if err != nil {
		t.Fatal(err)
	}

	req.AIRequestID = ar.ID
	req.Files = []FileStateInput{{Path: "a.go", Reviewed: bptr(true), Reason: "trivial"}}
	if resp := s.handleFileStates(ctx, req); !resp.OK {
		t.Fatalf("batch 1: %s", resp.Error)
	}
	req.Files = []FileStateInput{{Path: "a.go", Hidden: bptr(true), Reason: "also noise"}}
	if resp := s.handleFileStates(ctx, req); !resp.OK {
		t.Fatalf("batch 2: %s", resp.Error)
	}

	got, err := s.store.GetAIRequest(ctx, ar.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 1 {
		t.Fatalf("changes = %+v, want one merged entry for a.go", got.Changes)
	}
	c := got.Changes[0]
	if !reflect.DeepEqual(c.Prior, store.PriorState{}) {
		t.Fatalf("prior = %+v, want the FIRST (pre-request) prior", c.Prior)
	}
	if !c.Applied.Reviewed || !c.Applied.Hidden || c.Reason != "also noise" {
		t.Fatalf("applied = %+v reason = %q, want latest applied + reason", c.Applied, c.Reason)
	}

	// The recorded inverse undoes both batches in one restore.
	if err := s.store.RestoreFileStates(ctx, started.ReviewID, got.Changes); err != nil {
		t.Fatal(err)
	}
	states, _ := s.store.ListFileStates(ctx, started.ReviewID)
	if len(states) != 1 || states[0].Reviewed || states[0].Hidden {
		t.Fatalf("restored state = %+v, want pre-request defaults", states)
	}
}

func TestHandleFileStatesGuardsAIRequest(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)

	t.Run("foreign review", func(t *testing.T) {
		other, err := s.store.CreateReview(ctx, "sX", 0, "/elsewhere", "main", "base0")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := s.store.CreateAIRequest(ctx, other.ID, 1, "user", "p")
		if err != nil {
			t.Fatal(err)
		}
		r := req
		r.AIRequestID = foreign.ID
		r.Files = []FileStateInput{{Path: "a.go", Reviewed: bptr(true)}}
		if resp := s.handleFileStates(ctx, r); resp.OK || !strings.Contains(resp.Error, "does not belong") {
			t.Fatalf("ok=%v err=%q, want ownership rejection", resp.OK, resp.Error)
		}
	})

	t.Run("finished request", func(t *testing.T) {
		ar, err := s.store.CreateAIRequest(ctx, started.ReviewID, started.Version, "user", "p")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.store.TransitionAIRequest(ctx, ar.ID, "done", "did it", nil); err != nil {
			t.Fatal(err)
		}
		r := req
		r.AIRequestID = ar.ID
		r.Files = []FileStateInput{{Path: "a.go", Reviewed: bptr(true)}}
		if resp := s.handleFileStates(ctx, r); resp.OK || !strings.Contains(resp.Error, "pending or working") {
			t.Fatalf("ok=%v err=%q, want finished-request rejection", resp.OK, resp.Error)
		}
	})
}

func TestClaudeOriginEventsFilteredFromClaudeStream(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)

	req.Files = []FileStateInput{{Path: "a.go", Reviewed: bptr(true)}}
	if resp := s.handleFileStates(ctx, req); !resp.OK {
		t.Fatalf("file-states: %s", resp.Error)
	}

	// The browser stream (unfiltered) sees Claude's marks; the Claude-side
	// stream (excludeClaude) must not echo them back.
	if got := len(eventsOfType(t, s, started.ReviewID, store.EventFileStates, false)); got != 1 {
		t.Fatalf("unfiltered file.states = %d, want 1", got)
	}
	if got := len(eventsOfType(t, s, started.ReviewID, store.EventFileStates, true)); got != 0 {
		t.Fatalf("excludeClaude file.states = %d, want 0", got)
	}
	// System events (the organize request) stay visible on both streams.
	if got := len(eventsOfType(t, s, started.ReviewID, store.EventAIRequestCreated, true)); got != 1 {
		t.Fatalf("excludeClaude ai.request.created = %d, want 1", got)
	}
}

func TestHandleSubmitOrganization(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)

	chapters := []store.Chapter{{Title: "All", Summary: "everything", Files: []store.ChapterFile{
		{Path: "a.go", Risk: "low", Rationale: "r"},
		{Path: "b.go", Risk: "mechanical", Rationale: "r"},
	}}}

	t.Run("stale version_number rejected with the current one", func(t *testing.T) {
		r := req
		r.Organization = &store.Organization{Chapters: chapters}
		r.VersionNumber = started.Version + 1
		resp := s.handleSubmitOrganization(ctx, r)
		if resp.OK || !strings.Contains(resp.Error, "stale version_number") ||
			!strings.Contains(resp.Error, "current version is 1") {
			t.Fatalf("ok=%v err=%q, want stale rejection naming version 1", resp.OK, resp.Error)
		}
	})

	t.Run("incomplete coverage enumerated", func(t *testing.T) {
		r := req
		r.Organization = &store.Organization{Chapters: []store.Chapter{{Title: "Partial", Summary: "s",
			Files: []store.ChapterFile{{Path: "a.go", Risk: "low", Rationale: "r"}}}}}
		resp := s.handleSubmitOrganization(ctx, r)
		if resp.OK || !strings.Contains(resp.Error, "missing paths: b.go") {
			t.Fatalf("ok=%v err=%q, want missing-path enumeration", resp.OK, resp.Error)
		}
	})

	t.Run("exact coverage persists and emits", func(t *testing.T) {
		r := req
		r.Organization = &store.Organization{Chapters: chapters}
		r.VersionNumber = started.Version
		if resp := s.handleSubmitOrganization(ctx, r); !resp.OK {
			t.Fatalf("submit: %s", resp.Error)
		}
		v, _, err := s.store.LatestVersion(ctx, started.ReviewID)
		if err != nil {
			t.Fatal(err)
		}
		org, ok, err := s.store.GetOrganization(ctx, v.ID)
		if err != nil || !ok {
			t.Fatalf("get organization: ok=%v err=%v", ok, err)
		}
		if !reflect.DeepEqual(org.Chapters, chapters) {
			t.Fatalf("chapters = %+v, want %+v", org.Chapters, chapters)
		}
		events := eventsOfType(t, s, started.ReviewID, store.EventOrganizationUpdated, false)
		if len(events) != 1 || events[0].Origin != store.OriginClaude {
			t.Fatalf("organization.updated events = %+v, want one claude-origin event", events)
		}
	})
}

func TestStartUnmarksChangedFilesAcrossVersions(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)

	req.Files = []FileStateInput{
		{Path: "a.go", Reviewed: bptr(true)},
		{Path: "b.go", Reviewed: bptr(true), Hidden: bptr(true)},
	}
	if resp := s.handleFileStates(ctx, req); !resp.OK {
		t.Fatalf("mark: %s", resp.Error)
	}

	// a.go changes, b.go does not; the second start unmarks only a.go.
	writeFile(t, repo, "a.go", "package a\nfunc Changed() {}\n")
	second := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !second.OK {
		t.Fatalf("second start: %s", second.Error)
	}
	if second.ReviewID != started.ReviewID || second.Version != 2 {
		t.Fatalf("second start = %+v, want version 2 of the same review", second)
	}

	states, err := s.store.ListFileStates(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]store.FileState{}
	for _, st := range states {
		byPath[st.Path] = st
	}
	if byPath["a.go"].Reviewed {
		t.Fatal("a.go changed and must be unmarked")
	}
	if !byPath["b.go"].Reviewed || !byPath["b.go"].Hidden {
		t.Fatalf("b.go = %+v, want reviewed state and hidden flag carried forward", byPath["b.go"])
	}

	if got := len(eventsOfType(t, s, started.ReviewID, store.EventVersionCreated, false)); got != 2 {
		t.Fatalf("version.created events = %d, want one per start", got)
	}
	unmark := eventsOfType(t, s, started.ReviewID, store.EventFileStates, true) // origin system: visible to Claude
	if len(unmark) != 1 || unmark[0].Origin != store.OriginSystem || unmark[0].VersionNumber != 2 {
		t.Fatalf("unmark events = %+v, want one system file.states on v2", unmark)
	}
	var payload struct {
		States []struct {
			Path     string `json:"path"`
			Reviewed bool   `json:"reviewed"`
			Hidden   bool   `json:"hidden"`
		} `json:"states"`
	}
	if err := json.Unmarshal(unmark[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.States) != 1 || payload.States[0].Path != "a.go" || payload.States[0].Reviewed {
		t.Fatalf("unmark payload = %+v, want absolute a.go reviewed=false", payload.States)
	}

	// Each start queues a fresh system organize request.
	organize := eventsOfType(t, s, started.ReviewID, store.EventAIRequestCreated, false)
	if len(organize) != 2 {
		t.Fatalf("ai.request.created events = %d, want one per version", len(organize))
	}

	// get_review_files reflects the carried state on the new version.
	rf := s.handleReviewFiles(ctx, req)
	if !rf.OK {
		t.Fatalf("review-files: %s", rf.Error)
	}
	var listing struct {
		VersionNumber int `json:"version_number"`
		Files         []struct {
			Path     string `json:"path"`
			Status   string `json:"status"`
			Reviewed bool   `json:"reviewed"`
			Hidden   bool   `json:"hidden"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rf.ReviewFiles, &listing); err != nil {
		t.Fatal(err)
	}
	if listing.VersionNumber != 2 || len(listing.Files) != 2 {
		t.Fatalf("listing = %+v, want 2 files on version 2", listing)
	}
	for _, f := range listing.Files {
		switch f.Path {
		case "a.go":
			if f.Reviewed {
				t.Fatal("a.go listed as reviewed after unmark")
			}
		case "b.go":
			if !f.Reviewed || !f.Hidden {
				t.Fatalf("b.go = %+v, want reviewed+hidden", f)
			}
		default:
			t.Fatalf("unexpected path %q", f.Path)
		}
	}
}

func TestUpdateAIRequestEventCarriesCurrentVersion(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)
	ar, err := s.store.CreateAIRequest(ctx, started.ReviewID, started.Version, "user", "p")
	if err != nil {
		t.Fatal(err)
	}

	// A second version lands while the request is still in flight; the update
	// event must carry the current version, not the request's creation version.
	writeFile(t, repo, "a.go", "package a\nfunc Changed() {}\n")
	second := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !second.OK {
		t.Fatalf("second start: %s", second.Error)
	}

	r := req
	r.AIRequestID = ar.ID
	r.AIStatus = "done"
	if resp := s.handleUpdateAIRequest(ctx, r); !resp.OK {
		t.Fatalf("to done: %s", resp.Error)
	}

	updates := eventsOfType(t, s, started.ReviewID, store.EventAIRequestUpdated, false)
	if len(updates) != 1 {
		t.Fatalf("ai.request.updated events = %d, want 1", len(updates))
	}
	if updates[0].VersionNumber != second.Version {
		t.Fatalf("event version = %d, want current version %d", updates[0].VersionNumber, second.Version)
	}
	var payload struct {
		VersionNumber int `json:"version_number"`
		Request       struct {
			ID string `json:"id"`
		} `json:"request"`
	}
	if err := json.Unmarshal(updates[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.VersionNumber != second.Version || payload.Request.ID != strconv.FormatInt(ar.ID, 10) {
		t.Fatalf("payload = %+v, want version %d for request %d", payload, second.Version, ar.ID)
	}
}

func TestHandleUpdateAIRequestLifecycle(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)
	ar, err := s.store.CreateAIRequest(ctx, started.ReviewID, started.Version, "user", "p")
	if err != nil {
		t.Fatal(err)
	}

	r := req
	r.AIRequestID = ar.ID
	r.AIStatus = "working"
	if resp := s.handleUpdateAIRequest(ctx, r); !resp.OK {
		t.Fatalf("to working: %s", resp.Error)
	}
	r.AIStatus = "done"
	r.Summary = "marked nothing"
	r.Unmatched = []store.Unmatched{{Pattern: "everything", Why: "no match"}}
	if resp := s.handleUpdateAIRequest(ctx, r); !resp.OK {
		t.Fatalf("to done: %s", resp.Error)
	}
	// done is terminal for the MCP path; a repeat is refused by the guard.
	if resp := s.handleUpdateAIRequest(ctx, r); resp.OK {
		t.Fatal("done -> done must be refused")
	}
	// undone is REST-only, never a valid MCP status.
	r.AIStatus = "undone"
	if resp := s.handleUpdateAIRequest(ctx, r); resp.OK {
		t.Fatal("undone must be rejected at the daemon op")
	}

	updates := eventsOfType(t, s, started.ReviewID, store.EventAIRequestUpdated, false)
	if len(updates) != 2 {
		t.Fatalf("ai.request.updated events = %d, want 2", len(updates))
	}
	var payload struct {
		Request struct {
			Status    string            `json:"status"`
			Summary   string            `json:"summary"`
			Unmatched []store.Unmatched `json:"unmatched"`
		} `json:"request"`
	}
	if err := json.Unmarshal(updates[1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Request.Status != "done" || payload.Request.Summary != "marked nothing" || len(payload.Request.Unmatched) != 1 {
		t.Fatalf("done payload = %+v, want full updated request", payload.Request)
	}
}
