package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	ccevent "github.com/yasyf/cc-interact/event"

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

// testEvent projects a logged event with its version_number lifted out of the
// JSON payload (the events table no longer carries a version_number column).
type testEvent struct {
	Origin        string
	Type          string
	VersionNumber int
	Payload       json.RawMessage
}

func eventsOfType(t *testing.T, s *Server, reviewID, typ string, excludeAgent bool) []testEvent {
	t.Helper()
	events, err := s.cc.EventsSince(context.Background(), reviewID, 0, excludeAgent)
	if err != nil {
		t.Fatal(err)
	}
	var out []testEvent
	for _, e := range events {
		if e.Type != typ {
			continue
		}
		var p struct {
			VersionNumber int `json:"version_number"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		out = append(out, testEvent{Origin: e.Origin, Type: e.Type, VersionNumber: p.VersionNumber, Payload: e.Payload})
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
		other, err := s.createReview(ctx, "sX", 0, "/elsewhere", "main", "base0")
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

	t.Run("missing version_number rejected", func(t *testing.T) {
		r := req
		r.Organization = &store.Organization{Chapters: chapters}
		resp := s.handleSubmitOrganization(ctx, r)
		if resp.OK || !strings.Contains(resp.Error, "requires version_number") {
			t.Fatalf("ok=%v err=%q, want requires-version_number rejection", resp.OK, resp.Error)
		}
	})

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
		r.VersionNumber = started.Version
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
		if len(events) != 1 || events[0].Origin != ccevent.OriginAgent {
			t.Fatalf("organization.updated events = %+v, want one agent-origin event", events)
		}
	})
}

func TestReviewFilesIncludesPatchPath(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)

	v, _, err := s.store.LatestVersion(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if v.PatchPath == "" {
		t.Fatal("version stored without a patch path")
	}
	resp := s.handleReviewFiles(ctx, req)
	if !resp.OK {
		t.Fatalf("review-files: %s", resp.Error)
	}
	var rf struct {
		PatchPath string `json:"patch_path"`
	}
	if err := json.Unmarshal(resp.ReviewFiles, &rf); err != nil {
		t.Fatal(err)
	}
	if rf.PatchPath != v.PatchPath {
		t.Fatalf("patch_path = %q, want the stored %q", rf.PatchPath, v.PatchPath)
	}
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

func TestStartReturnsEagerOrganizeRequest(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	_, started := startedReview(t, s, repo)

	if len(started.AIRequests) != 1 {
		t.Fatalf("fresh start must re-offer exactly the organize request, got %d", len(started.AIRequests))
	}
	var ar struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(started.AIRequests[0], &ar); err != nil {
		t.Fatalf("ai_request is not valid JSON: %v", err)
	}
	if ar.Source != "system" || ar.Prompt != organizePrompt {
		t.Fatalf("ai_request = %+v, want the system organize request", ar)
	}

	// The dedupe contract: the response carries the same bytes (and so the same
	// id) as the request object inside the ai.request.created event payload.
	created := eventsOfType(t, s, started.ReviewID, store.EventAIRequestCreated, false)
	if len(created) != 1 {
		t.Fatalf("ai.request.created events = %d, want 1", len(created))
	}
	var payload struct {
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(created[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload.Request, started.AIRequests[0]) {
		t.Fatalf("event request = %s\nresponse ai_request = %s\nwant byte-identical", payload.Request, started.AIRequests[0])
	}

	// A changed tree lands a new version with a fresh request id.
	writeFile(t, repo, "a.go", "package a\nfunc Changed() {}\n")
	second := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !second.OK || len(second.AIRequests) != 1 {
		t.Fatalf("second start: ok=%v err=%q ai_requests=%v", second.OK, second.Error, second.AIRequests)
	}
	var ar2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(second.AIRequests[0], &ar2); err != nil {
		t.Fatal(err)
	}
	if ar2.ID == ar.ID {
		t.Fatalf("changed-tree start reused organize request id %q", ar.ID)
	}

	// An unchanged resume reuses the version and rescues the still-open
	// organize request: same id, no fresh row.
	third := s.handleStart(ctx, Request{Session: "sA", ClaudePID: 100, Cwd: repo})
	if !third.OK || !third.Resumed {
		t.Fatalf("resume: ok=%v resumed=%v err=%q", third.OK, third.Resumed, third.Error)
	}
	if len(third.AIRequests) != 1 {
		t.Fatalf("unchanged resume re-offered %d requests, want the single still-open organize", len(third.AIRequests))
	}
	var ar3 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(third.AIRequests[0], &ar3); err != nil {
		t.Fatal(err)
	}
	if ar3.ID != ar2.ID {
		t.Fatalf("unchanged resume returned request %q, want the still-open %q", ar3.ID, ar2.ID)
	}
}

func TestStartUnchangedResumeRescuesOrganize(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		organized bool   // submit an organization for the latest version before resuming
		sysStatus string // move the eager system organize request here ("" keeps pending)
		userOpen  bool   // add an open user-origin request before resuming
		want      string // closed | rescued | fresh
	}{
		{name: "organized closes pending", organized: true, want: "closed"},
		{name: "organized closes working", organized: true, sysStatus: "working", want: "closed"},
		{name: "organized re-offers open user request", organized: true, userOpen: true, want: "user-only"},
		{name: "pending rescued with the same id", want: "rescued"},
		{name: "working rescued with the same id", sysStatus: "working", want: "rescued"},
		{name: "failed gets a fresh request", sysStatus: "failed", want: "fresh"},
		{name: "open user request untouched", sysStatus: "failed", userOpen: true, want: "fresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := testServer(t)
			req, started := startedReview(t, s, repo)
			var sys struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(started.AIRequests[0], &sys); err != nil {
				t.Fatal(err)
			}
			sysID, err := strconv.ParseInt(sys.ID, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if tc.sysStatus != "" {
				if _, err := s.store.TransitionAIRequest(ctx, sysID, tc.sysStatus, "", nil); err != nil {
					t.Fatal(err)
				}
			}
			if tc.organized {
				submitOrg(t, s, req, started.Version, "a.go", "b.go")
			}
			var userID int64
			if tc.userOpen {
				ur, err := s.store.CreateAIRequest(ctx, started.ReviewID, started.Version, store.OriginUser, "mark a.go")
				if err != nil {
					t.Fatal(err)
				}
				userID = ur.ID
			}
			createdBefore := len(eventsOfType(t, s, started.ReviewID, store.EventAIRequestCreated, false))
			updatedBefore := len(eventsOfType(t, s, started.ReviewID, store.EventAIRequestUpdated, false))

			resumed := s.handleStart(ctx, req)
			if !resumed.OK || !resumed.Resumed || resumed.Version != started.Version {
				t.Fatalf("resume: ok=%v resumed=%v version=%d err=%q", resumed.OK, resumed.Resumed, resumed.Version, resumed.Error)
			}

			switch tc.want {
			case "closed":
				if len(resumed.AIRequests) != 0 {
					t.Fatalf("organized resume re-offered %v, want none", resumed.AIRequests)
				}
				got, err := s.store.GetAIRequest(ctx, sysID)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != "done" || !strings.Contains(got.Summary, "already organized") {
					t.Fatalf("system request status=%q summary=%q, want done via already-organized", got.Status, got.Summary)
				}
				if got := len(eventsOfType(t, s, started.ReviewID, store.EventAIRequestUpdated, false)); got != updatedBefore+1 {
					t.Fatalf("ai.request.updated events = %d, want exactly %d", got, updatedBefore+1)
				}
			case "user-only":
				// The incident: an organized resume closes the system organize but
				// re-offers the human's still-open AI-bar request so the freshly
				// attached session dispatches it.
				if len(resumed.AIRequests) != 1 {
					t.Fatalf("organized resume re-offered %d requests, want only the open user request", len(resumed.AIRequests))
				}
				var ar struct {
					ID     string `json:"id"`
					Source string `json:"source"`
					Status string `json:"status"`
				}
				if err := json.Unmarshal(resumed.AIRequests[0], &ar); err != nil {
					t.Fatal(err)
				}
				if ar.ID != strconv.FormatInt(userID, 10) || ar.Source != "user" || ar.Status != "pending" {
					t.Fatalf("re-offered request = %+v, want the open user request %d", ar, userID)
				}
				sysReq, err := s.store.GetAIRequest(ctx, sysID)
				if err != nil {
					t.Fatal(err)
				}
				if sysReq.Status != "done" {
					t.Fatalf("system organize status = %q, want done (closed, not re-offered)", sysReq.Status)
				}
			case "rescued":
				if len(resumed.AIRequests) != 1 {
					t.Fatalf("rescued resume re-offered %d requests, want the single rescued organize", len(resumed.AIRequests))
				}
				var ar struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}
				if err := json.Unmarshal(resumed.AIRequests[0], &ar); err != nil {
					t.Fatal(err)
				}
				if ar.ID != sys.ID {
					t.Fatalf("rescued id = %q, want the open request %q", ar.ID, sys.ID)
				}
				wantStatus := tc.sysStatus
				if wantStatus == "" {
					wantStatus = "pending"
				}
				if ar.Status != wantStatus {
					t.Fatalf("rescued status = %q, want %q", ar.Status, wantStatus)
				}
				requests, err := s.store.ListAIRequests(ctx, started.ReviewID)
				if err != nil {
					t.Fatal(err)
				}
				if len(requests) != 1 {
					t.Fatalf("requests = %d, want the single rescued row", len(requests))
				}
				if got := len(eventsOfType(t, s, started.ReviewID, store.EventAIRequestCreated, false)); got != createdBefore {
					t.Fatalf("ai.request.created events = %d, want unchanged %d (no re-emit)", got, createdBefore)
				}
			case "fresh":
				var ar struct {
					ID     string `json:"id"`
					Source string `json:"source"`
					Status string `json:"status"`
				}
				// AIRequests[0] is the freshly created system organize (newest);
				// an open user request, if any, trails it.
				if err := json.Unmarshal(resumed.AIRequests[0], &ar); err != nil {
					t.Fatal(err)
				}
				if ar.ID == sys.ID {
					t.Fatalf("fresh resume reused finished request id %q", ar.ID)
				}
				if ar.Source != "system" || ar.Status != "pending" {
					t.Fatalf("fresh request = %+v, want a pending system request", ar)
				}
				created := eventsOfType(t, s, started.ReviewID, store.EventAIRequestCreated, false)
				if len(created) != createdBefore+1 {
					t.Fatalf("ai.request.created events = %d, want %d", len(created), createdBefore+1)
				}
				var payload struct {
					Request json.RawMessage `json:"request"`
				}
				if err := json.Unmarshal(created[len(created)-1].Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(payload.Request, resumed.AIRequests[0]) {
					t.Fatalf("event request = %s\nresponse ai_request = %s\nwant byte-identical", payload.Request, resumed.AIRequests[0])
				}
				if tc.userOpen {
					got, err := s.store.GetAIRequest(ctx, userID)
					if err != nil {
						t.Fatal(err)
					}
					if got.Status != "pending" {
						t.Fatalf("user request status = %q, want untouched pending", got.Status)
					}
					// The open user request trails the fresh system organize in the
					// re-offer, so a freshly attached session dispatches both.
					if len(resumed.AIRequests) != 2 {
						t.Fatalf("re-offered %d requests, want system organize + open user request", len(resumed.AIRequests))
					}
				}
			}
		})
	}
}

func TestSweepStalePending(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	_, started := startedReview(t, s, repo)

	// A human AI-bar request no live session ever dispatched.
	userReq, err := s.store.CreateAIRequest(ctx, started.ReviewID, started.Version, store.OriginUser, "mark all mechanical changes as viewed")
	if err != nil {
		t.Fatal(err)
	}
	// A working user request must survive the sweep (only pending is failed).
	working, err := s.store.CreateAIRequest(ctx, started.ReviewID, started.Version, store.OriginUser, "in flight")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.TransitionAIRequest(ctx, working.ID, "working", "", nil); err != nil {
		t.Fatal(err)
	}
	updatedBefore := len(eventsOfType(t, s, started.ReviewID, store.EventAIRequestUpdated, false))

	// A cutoff in the future treats every still-pending request as stale.
	if err := s.sweepStalePending(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got, err := s.store.GetAIRequest(ctx, userReq.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || !strings.Contains(got.Summary, "Resume with /cc-review:start") {
		t.Fatalf("user request status=%q summary=%q, want failed with a resume hint", got.Status, got.Summary)
	}
	if w, _ := s.store.GetAIRequest(ctx, working.ID); w.Status != "working" {
		t.Fatalf("working request status=%q, want untouched working", w.Status)
	}
	// The system organize (source=system) is never swept.
	var sys struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(started.AIRequests[0], &sys); err != nil {
		t.Fatal(err)
	}
	sysID, _ := strconv.ParseInt(sys.ID, 10, 64)
	if sr, _ := s.store.GetAIRequest(ctx, sysID); sr.Status != "pending" {
		t.Fatalf("system organize status=%q, want untouched pending", sr.Status)
	}
	if updates := eventsOfType(t, s, started.ReviewID, store.EventAIRequestUpdated, false); len(updates) != updatedBefore+1 {
		t.Fatalf("ai.request.updated events = %d, want exactly one more (%d)", len(updates), updatedBefore+1)
	}

	// Idempotent: a second sweep finds nothing still pending and emits nothing.
	if err := s.sweepStalePending(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if again := eventsOfType(t, s, started.ReviewID, store.EventAIRequestUpdated, false); len(again) != updatedBefore+1 {
		t.Fatalf("second sweep emitted more events: %d", len(again))
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

// submitOrg submits a one-chapter organization covering paths on the review's
// given version.
func submitOrg(t *testing.T, s *Server, req Request, version int, paths ...string) store.Organization {
	t.Helper()
	files := make([]store.ChapterFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, store.ChapterFile{Path: p, Risk: "low", Rationale: "r"})
	}
	org := store.Organization{Chapters: []store.Chapter{{Title: "All", Summary: "s", Files: files}}}
	r := req
	r.Organization = &org
	r.VersionNumber = version
	if resp := s.handleSubmitOrganization(context.Background(), r); !resp.OK {
		t.Fatalf("submit organization: %s", resp.Error)
	}
	return org
}

func TestStartCarriesOrganizationForwardOnRevert(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)
	org := submitOrg(t, s, req, started.Version, "a.go", "b.go")

	// v2: a changed tree queues a fresh organize request that never completes.
	writeFile(t, repo, "a.go", "package a\nfunc Changed() {}\n")
	second := s.handleStart(ctx, req)
	if !second.OK || second.Version != 2 {
		t.Fatalf("second start: ok=%v version=%d err=%q", second.OK, second.Version, second.Error)
	}
	// v3: reverting restores v1's per-file fingerprints exactly, so the daemon
	// reattaches v1's organization instead of dispatching an agent.
	writeFile(t, repo, "a.go", "package a\n")
	third := s.handleStart(ctx, req)
	if !third.OK || third.Version != 3 {
		t.Fatalf("third start: ok=%v version=%d err=%q", third.OK, third.Version, third.Error)
	}
	if len(third.AIRequests) != 0 {
		t.Fatalf("carried start re-offered %v, want none", third.AIRequests)
	}
	v3, _, err := s.store.LatestVersion(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.store.GetOrganization(ctx, v3.ID)
	if err != nil || !ok {
		t.Fatalf("v3 organization: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, org) {
		t.Fatalf("carried organization = %+v, want %+v", got, org)
	}
	if got := countEvents(t, s, started.ReviewID, store.EventAIRequestCreated); got != 2 {
		t.Fatalf("ai.request.created events = %d, want 2 (v1 + v2 only)", got)
	}
	updated := eventsOfType(t, s, started.ReviewID, store.EventOrganizationUpdated, false)
	if len(updated) != 2 {
		t.Fatalf("organization.updated events = %d, want submit + carry", len(updated))
	}
	if carried := updated[1]; carried.Origin != ccevent.OriginAgent || carried.VersionNumber != 3 {
		t.Fatalf("carried event origin=%s version=%d, want agent origin on version 3", carried.Origin, carried.VersionNumber)
	}
	// Claude-origin keeps the carried event off the Claude-side stream.
	if got := len(eventsOfType(t, s, started.ReviewID, store.EventOrganizationUpdated, true)); got != 0 {
		t.Fatalf("excludeClaude organization.updated = %d, want 0", got)
	}
	// The carry closes both stranded system organize requests; nothing is left
	// to keep the UI's "organizing…" chip lit.
	requests, err := s.store.ListAIRequests(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ar := range requests {
		if ar.Source != store.OriginSystem {
			continue
		}
		if ar.Status != "done" || !strings.Contains(ar.Summary, "carried to version 3") {
			t.Fatalf("system request %d: status=%q summary=%q, want done via carry", ar.ID, ar.Status, ar.Summary)
		}
	}
}

func TestStartDoesNotCarryAcrossAChangedDiff(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	req, started := startedReview(t, s, repo)
	submitOrg(t, s, req, started.Version, "a.go", "b.go")

	writeFile(t, repo, "a.go", "package a\nfunc Changed() {}\n")
	second := s.handleStart(ctx, req)
	if !second.OK || len(second.AIRequests) != 1 {
		t.Fatalf("second start: ok=%v err=%q ai_requests=%v", second.OK, second.Error, second.AIRequests)
	}
	v2, _, err := s.store.LatestVersion(ctx, started.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.GetOrganization(ctx, v2.ID); err != nil || ok {
		t.Fatalf("v2 organization: ok=%v err=%v, want absent", ok, err)
	}
	if got := len(eventsOfType(t, s, started.ReviewID, store.EventOrganizationUpdated, false)); got != 1 {
		t.Fatalf("organization.updated events = %d, want only the v1 submit", got)
	}
}

func TestReviewFilesIncludesAnnotatedOrganization(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	// A committed file with enough lines that a later git mv stays above the
	// rename-similarity threshold; the small touch puts it in the v1 diff.
	libBody := "package lib\n" + strings.Repeat("// padding\n", 20)
	writeFile(t, repo, "lib.go", libBody)
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "lib")
	writeFile(t, repo, "lib.go", libBody+"func Touched() {}\n")
	req, started := startedReview(t, s, repo)

	type rfFile struct {
		Path      string `json:"path"`
		Risk      string `json:"risk"`
		Rationale string `json:"rationale"`
		Delta     string `json:"delta"`
		Now       string `json:"now"`
	}
	type rfOrg struct {
		BasisVersion int `json:"basis_version"`
		Chapters     []struct {
			Title string   `json:"title"`
			Files []rfFile `json:"files"`
		} `json:"chapters"`
		NewPaths []string `json:"new_paths"`
	}
	reviewFiles := func() (int, *rfOrg) {
		t.Helper()
		resp := s.handleReviewFiles(ctx, req)
		if !resp.OK {
			t.Fatalf("review-files: %s", resp.Error)
		}
		var rf struct {
			VersionNumber int    `json:"version_number"`
			Organization  *rfOrg `json:"organization"`
		}
		if err := json.Unmarshal(resp.ReviewFiles, &rf); err != nil {
			t.Fatal(err)
		}
		return rf.VersionNumber, rf.Organization
	}

	if _, org := reviewFiles(); org != nil {
		t.Fatalf("unorganized review returned organization %+v", org)
	}

	submitOrg(t, s, req, started.Version, "a.go", "b.go", "lib.go")
	version, org := reviewFiles()
	if org == nil || org.BasisVersion != version {
		t.Fatalf("live organization = %+v at version %d, want basis == version", org, version)
	}
	for _, f := range org.Chapters[0].Files {
		if f.Delta != "" {
			t.Fatalf("live organization annotated %s as %q", f.Path, f.Delta)
		}
	}
	if len(org.NewPaths) != 0 {
		t.Fatalf("live organization new_paths = %v, want none", org.NewPaths)
	}

	// v2: a.go changes, b.go vanishes, lib.go moves, c.go is new.
	writeFile(t, repo, "a.go", "package a\nfunc Changed() {}\n")
	if err := os.Remove(filepath.Join(repo, "b.go")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "mv", "lib.go", "moved.go")
	writeFile(t, repo, "c.go", "package c\n")
	if resp := s.handleStart(ctx, req); !resp.OK {
		t.Fatalf("second start: %s", resp.Error)
	}

	version, org = reviewFiles()
	if version != 2 || org == nil || org.BasisVersion != 1 {
		t.Fatalf("stale organization = %+v at version %d, want basis 1", org, version)
	}
	deltas := make(map[string]rfFile, len(org.Chapters[0].Files))
	for _, f := range org.Chapters[0].Files {
		deltas[f.Path] = f
	}
	if f := deltas["a.go"]; f.Delta != "changed" {
		t.Fatalf("a.go delta = %q, want changed", f.Delta)
	}
	if f := deltas["b.go"]; f.Delta != "removed" {
		t.Fatalf("b.go delta = %q, want removed", f.Delta)
	}
	if f := deltas["lib.go"]; f.Delta != "moved" || f.Now != "moved.go" {
		t.Fatalf("lib.go delta = %q now = %q, want moved to moved.go", f.Delta, f.Now)
	}
	if !reflect.DeepEqual(org.NewPaths, []string{"c.go"}) {
		t.Fatalf("new_paths = %v, want [c.go]", org.NewPaths)
	}
}

// A basis D x.go + A p.go pair that git later pairs as R x.go -> p.go must not
// produce two org entries claiming p.go (the agent would submit one current
// path in two chapters and Validate would reject): the direct match wins and
// the origin-joined entry degrades to removed.
func TestOrganizationContextDirectMatchWinsOverOriginJoin(t *testing.T) {
	basis := store.Version{VersionNumber: 1, FilesJSON: `[
		{"path":"p.go","status":"A","fingerprint":"fpA"},
		{"path":"x.go","status":"D","fingerprint":"fpD"}
	]`}
	current := store.Version{VersionNumber: 2, FilesJSON: `[
		{"path":"p.go","old_path":"x.go","status":"R","fingerprint":"fpR"}
	]`}
	org := store.Organization{Chapters: []store.Chapter{{
		Title: "Both",
		Files: []store.ChapterFile{
			{Path: "p.go", Risk: "low", Rationale: "added"},
			{Path: "x.go", Risk: "low", Rationale: "deleted"},
		},
	}}}

	out, err := organizationContext(org, basis, current)
	if err != nil {
		t.Fatal(err)
	}
	files := out["chapters"].([]map[string]any)[0]["files"].([]map[string]any)
	deltas := make(map[string]map[string]any, len(files))
	for _, f := range files {
		deltas[f["path"].(string)] = f
	}
	if d := deltas["p.go"]["delta"]; d != "changed" {
		t.Fatalf("p.go delta = %v, want changed", d)
	}
	if d := deltas["x.go"]["delta"]; d != "removed" {
		t.Fatalf("x.go delta = %v, want removed (p.go is already direct-claimed)", d)
	}
	if now, ok := deltas["x.go"]["now"]; ok {
		t.Fatalf("x.go now = %v, want no rename claim", now)
	}
	if newPaths := out["new_paths"].([]string); len(newPaths) != 0 {
		t.Fatalf("new_paths = %v, want none (p.go is claimed)", newPaths)
	}
}
