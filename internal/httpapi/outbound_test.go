package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	ccstore "github.com/yasyf/cc-interact/store"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/github/githubtest"
	"github.com/yasyf/cc-review/internal/outbound"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/testhome"
	"github.com/yasyf/cc-review/internal/web"
)

const (
	prRepo     = "o/r"
	prNumber   = 7
	prHeadSHA  = "head7sha"
	userToken  = "user-token"
	viewer     = "alice"
	commentsAt = "/repos/o/r/pulls/7/comments"
	issuesAt   = "/repos/o/r/issues/7/comments"
	replyOp    = "graphql:AddThreadReply"
)

type prServer struct {
	st     *store.Store
	cc     *ccstore.Store
	srv    *httptest.Server
	gh     *githubtest.Server
	sync   *outbound.Syncer
	review store.Review
	sec    store.Section
}

func newPRServer(t *testing.T, viewerIsAuthor bool) *prServer {
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
	gh := githubtest.New(t)
	gh.Login(userToken, viewer)
	author := "bob"
	if viewerIsAuthor {
		author = viewer
	}
	gh.AddPR(github.Repo{Owner: "o", Name: "r"}, github.PullRequest{Number: prNumber, AuthorLogin: author, HeadRefOid: prHeadSHA})
	syncer := outbound.New(cc.DB, cc.AppendEvent, gh.Client(userToken),
		func(context.Context, github.Repo) (*github.Client, error) {
			return nil, errors.New("no app in rest tests")
		})
	mux := http.NewServeMux()
	RESTMount(mux, http.NewServeMux(), Deps{
		DB: cc.DB, Decisions: ledger, Log: log.New(io.Discard, "", 0),
		Append: cc.AppendEvent, ConsumerConnected: func(string) bool { return false },
		Outbound: syncer, Dist: web.Dist(),
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st := store.New(cc.DB())
	ctx := t.Context()
	sub, err := ccstore.NewSubjectStore(st.DB()).Create(ctx, store.NewSlugHash(), store.ReviewSlug(store.NewSlugHash()), "s1", "/repo", 100, "open")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetReviewKind(ctx, sub.ID, store.ReviewKindPR, prRepo, prNumber); err != nil {
		t.Fatal(err)
	}
	_, sections, err := st.CreateVersion(ctx, sub.ID, "feature", prHeadSHA, "",
		[]store.SectionInput{{Position: 0, Branch: "feature", ParentBranch: "main", BaseRef: "base7sha", HeadRef: prHeadSHA, FilesJSON: "[]", PRNumber: prNumber}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertPullRequest(ctx, store.PullRequest{ReviewID: sub.ID, Number: prNumber, HeadSHA: prHeadSHA, ViewerIsAuthor: viewerIsAuthor}); err != nil {
		t.Fatal(err)
	}
	review, err := st.GetReview(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &prServer{st: st, cc: cc, srv: srv, gh: gh, sync: syncer, review: review, sec: sections[0]}
}

// settle waits for every GitHub write already queued for the review.
func (p *prServer) settle(t *testing.T) {
	t.Helper()
	if err := p.sync.Serialize(t.Context(), p.review.ID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func (p *prServer) comment(t *testing.T, body map[string]any) int64 {
	t.Helper()
	req := map[string]any{
		"sectionId": strconv.FormatInt(p.sec.ID, 10), "filePath": "a.go", "side": "additions",
		"range": map[string]any{"start": 3, "end": 5}, "body": "nit",
	}
	for k, v := range body {
		req[k] = v
	}
	resp := postJSON(t, p.srv.URL+"/api/comments", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create comment status = %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	id, err := strconv.ParseInt(out.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (p *prServer) getComment(t *testing.T, id int64) store.Comment {
	t.Helper()
	c, err := p.st.GetComment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func syncedStates(t *testing.T, cc *ccstore.Store, reviewID string) []string {
	t.Helper()
	evs, err := cc.EventsSince(context.Background(), reviewID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.Type != store.EventCommentSynced {
			continue
		}
		var p struct {
			SyncState string `json:"syncState"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p.SyncState)
	}
	return out
}

func putJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, url, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func writesTo(gh *githubtest.Server, path string) []githubtest.Write {
	var out []githubtest.Write
	for _, w := range gh.Writes() {
		if w.Path == path {
			out = append(out, w)
		}
	}
	return out
}

func TestPRCommentPostsAsUserAndStoresRemoteID(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  map[string]any
		want map[string]any
	}{
		{
			name: "line range",
			req:  map[string]any{},
			want: map[string]any{"commit_id": prHeadSHA, "path": "a.go", "body": "nit", "line": float64(5), "side": "RIGHT", "start_line": float64(3), "start_side": "RIGHT"},
		},
		{
			name: "deleted line",
			req:  map[string]any{"side": "deletions", "range": map[string]any{"start": 4, "end": 4}},
			want: map[string]any{"commit_id": prHeadSHA, "path": "a.go", "body": "nit", "line": float64(4), "side": "LEFT"},
		},
		{
			name: "file subject",
			req:  map[string]any{"subject": "file", "range": map[string]any{"start": 0, "end": 0}},
			want: map[string]any{"commit_id": prHeadSHA, "path": "a.go", "body": "nit", "subject_type": "file"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPRServer(t, false)
			id := p.comment(t, tc.req)
			p.settle(t)

			reqs := writesTo(p.gh, commentsAt)
			if len(reqs) != 1 {
				t.Fatalf("GitHub saw %d comment posts, want 1", len(reqs))
			}
			if reqs[0].Token != userToken {
				t.Fatalf("token = %q, want the user token", reqs[0].Token)
			}
			if !reflect.DeepEqual(reqs[0].Body, tc.want) {
				t.Fatalf("posted body = %v, want %v", reqs[0].Body, tc.want)
			}
			c := p.getComment(t, id)
			if c.SyncState != store.SyncSynced || c.SyncError != "" || c.RemoteID == "" || c.RemoteThreadID == "" || c.RemoteURL == "" {
				t.Fatalf("comment after sync = %+v, want synced with remote ids", c)
			}
			byRemote, err := p.st.CommentByRemoteID(context.Background(), p.review.ID, c.RemoteID)
			if err != nil || byRemote.ID != id {
				t.Fatalf("CommentByRemoteID(%s) = %d, %v; want %d", c.RemoteID, byRemote.ID, err, id)
			}
			if got := syncedStates(t, p.cc, p.review.ID); !reflect.DeepEqual(got, []string{store.SyncSynced}) {
				t.Fatalf("comment.synced states = %v, want [synced]", got)
			}
		})
	}
}

func TestPRCommentCreatedEventCarriesPosting(t *testing.T) {
	p := newPRServer(t, false)
	p.comment(t, nil)
	p.settle(t)
	evs, err := p.cc.EventsSince(context.Background(), p.review.ID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Comment struct {
			SyncState string `json:"syncState"`
		} `json:"comment"`
	}
	if evs[0].Type != store.EventCommentCreated {
		t.Fatalf("first event = %s, want comment.created", evs[0].Type)
	}
	if err := json.Unmarshal(evs[0].Payload, &created); err != nil {
		t.Fatal(err)
	}
	if created.Comment.SyncState != store.SyncPosting {
		t.Fatalf("comment.created syncState = %q, want posting", created.Comment.SyncState)
	}
}

func TestPRCommentFailureThenRetry(t *testing.T) {
	p := newPRServer(t, false)
	p.gh.FailNext(commentsAt, http.StatusUnprocessableEntity, "Validation Failed")
	id := p.comment(t, nil)
	p.settle(t)

	c := p.getComment(t, id)
	if c.SyncState != store.SyncFailed || !strings.Contains(c.SyncError, "422") || c.RemoteID != "" {
		t.Fatalf("comment after failed post = %+v, want failed with a 422 error and no remote id", c)
	}

	resp := postJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10)+"/retry", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", resp.StatusCode)
	}
	p.settle(t)
	c = p.getComment(t, id)
	if c.SyncState != store.SyncSynced || c.SyncError != "" || c.RemoteID == "" {
		t.Fatalf("comment after retry = %+v, want synced", c)
	}
	if got := syncedStates(t, p.cc, p.review.ID); !reflect.DeepEqual(got, []string{store.SyncFailed, store.SyncPosting, store.SyncSynced}) {
		t.Fatalf("comment.synced states = %v", got)
	}

	resp = postJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10)+"/retry", map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("retry of a synced comment status = %d, want 409", resp.StatusCode)
	}
}

func TestPRReplyFailureThenRetry(t *testing.T) {
	p := newPRServer(t, false)
	id := p.comment(t, nil)
	p.settle(t)
	thread := p.getComment(t, id).RemoteThreadID

	p.gh.FailNext(replyOp, http.StatusBadGateway, "Bad Gateway")
	resp := postJSON(t, p.srv.URL+"/api/replies/"+strconv.FormatInt(id, 10), map[string]any{"body": "fixed"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reply status = %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	p.settle(t)
	replyID, _ := strconv.ParseInt(out.ID, 10, 64)
	r, err := p.st.GetReply(context.Background(), replyID)
	if err != nil {
		t.Fatal(err)
	}
	if r.SyncState != store.SyncFailed || r.SyncError == "" {
		t.Fatalf("reply after failed post = %+v, want failed", r)
	}

	resp = postJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10)+"/retry", map[string]any{"replyId": out.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d", resp.StatusCode)
	}
	p.settle(t)
	if r, err = p.st.GetReply(context.Background(), replyID); err != nil {
		t.Fatal(err)
	}
	if r.SyncState != store.SyncSynced || r.RemoteID == "" || r.RemoteURL == "" {
		t.Fatalf("reply after retry = %+v, want synced", r)
	}
	reqs := writesTo(p.gh, replyOp)
	if len(reqs) != 1 || reqs[0].Token != userToken || reqs[0].Body["body"] != "fixed" || reqs[0].Body["thread"] != thread {
		t.Fatalf("reply writes = %+v, want one user-token reply on %s", reqs, thread)
	}
}

func TestPRResolveAndReopenMirrorTheThread(t *testing.T) {
	p := newPRServer(t, false)
	id := p.comment(t, nil)
	p.settle(t)
	thread := p.getComment(t, id).RemoteThreadID

	for _, status := range []string{"resolved", "open"} {
		resp := putJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10), map[string]any{"status": status})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT status=%s = %d", status, resp.StatusCode)
		}
		p.settle(t)
		if c := p.getComment(t, id); c.SyncState != store.SyncSynced || c.Status != status {
			t.Fatalf("comment after %s = %+v, want synced", status, c)
		}
	}
	var got []string
	for _, w := range p.gh.Writes() {
		if strings.HasPrefix(w.Path, "graphql:") && w.Body["id"] == thread && w.Token == userToken {
			got = append(got, w.Path)
		}
	}
	if want := []string{"graphql:ResolveThread", "graphql:UnresolveThread"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("thread writes = %v, want %v", got, want)
	}
}

func TestPRConversationCommentPostsAsIssueComments(t *testing.T) {
	p := newPRServer(t, false)
	id := p.comment(t, map[string]any{"subject": "file", "filePath": "", "range": map[string]any{"start": 0, "end": 0}, "body": "overall: nice"})
	p.settle(t)
	c := p.getComment(t, id)
	if c.SyncState != store.SyncSynced || c.RemoteID == "" || c.RemoteThreadID != "" {
		t.Fatalf("conversation comment = %+v, want synced with no thread", c)
	}

	resp := postJSON(t, p.srv.URL+"/api/replies/"+strconv.FormatInt(id, 10), map[string]any{"body": "agreed"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reply status = %d", resp.StatusCode)
	}
	resp = putJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10), map[string]any{"status": "resolved"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve status = %d", resp.StatusCode)
	}
	p.settle(t)

	issues := writesTo(p.gh, issuesAt)
	bodies := make([]any, 0, len(issues))
	for _, w := range issues {
		bodies = append(bodies, w.Body["body"])
	}
	if want := []any{"overall: nice", "agreed"}; !reflect.DeepEqual(bodies, want) {
		t.Fatalf("issue comments = %v, want %v", bodies, want)
	}
	if n := len(p.gh.Writes()); n != 2 {
		t.Fatalf("GitHub writes = %d, want only the two issue comments", n)
	}
	if c := p.getComment(t, id); c.Status != "resolved" || c.SyncState != store.SyncSynced {
		t.Fatalf("conversation comment after resolve = %+v, want resolved locally and still synced", c)
	}
}

func TestPRLineCommentNeedsAPath(t *testing.T) {
	p := newPRServer(t, false)
	resp := postJSON(t, p.srv.URL+"/api/comments", map[string]any{
		"sectionId": strconv.FormatInt(p.sec.ID, 10), "filePath": "",
		"side": "additions", "range": map[string]any{"start": 1, "end": 1}, "body": "x",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPRCommentBodyEditRefused(t *testing.T) {
	p := newPRServer(t, false)
	id := p.comment(t, nil)
	p.settle(t)
	resp := putJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10), map[string]any{"body": "changed"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("body edit status = %d, want 409", resp.StatusCode)
	}
	if c := p.getComment(t, id); c.Body != "nit" {
		t.Fatalf("body = %q, want unchanged", c.Body)
	}
}

func TestPRSubmitVerdicts(t *testing.T) {
	reviewsAt := "/repos/o/r/pulls/7/reviews"
	for _, tc := range []struct {
		name           string
		viewerIsAuthor bool
		req            map[string]any
		status         int
		posted         []map[string]any
	}{
		{
			name: "approve", req: map[string]any{"verdict": "APPROVE", "summary": "lgtm"}, status: http.StatusOK,
			posted: []map[string]any{{"event": "APPROVE", "body": "lgtm"}},
		},
		{
			name: "request changes", req: map[string]any{"verdict": "REQUEST_CHANGES", "summary": "see threads"}, status: http.StatusOK,
			posted: []map[string]any{{"event": "REQUEST_CHANGES", "body": "see threads"}},
		},
		{
			name: "comment with summary", req: map[string]any{"summary": "thoughts"}, status: http.StatusOK,
			posted: []map[string]any{{"event": "COMMENT", "body": "thoughts"}},
		},
		{name: "bare comment posts nothing", req: map[string]any{"verdict": "COMMENT"}, status: http.StatusOK},
		{name: "own PR approve", viewerIsAuthor: true, req: map[string]any{"verdict": "APPROVE"}, status: http.StatusBadRequest},
		{name: "own PR request changes", viewerIsAuthor: true, req: map[string]any{"verdict": "REQUEST_CHANGES", "summary": "x"}, status: http.StatusBadRequest},
		{
			name: "own PR comment", viewerIsAuthor: true, req: map[string]any{"verdict": "COMMENT", "summary": "notes"}, status: http.StatusOK,
			posted: []map[string]any{{"event": "COMMENT", "body": "notes"}},
		},
		{name: "unknown verdict", req: map[string]any{"verdict": "MERGE"}, status: http.StatusBadRequest},
		{name: "unknown version", req: map[string]any{"verdict": "APPROVE", "versionNumber": 9}, status: http.StatusBadRequest},
		{name: "no version", req: map[string]any{"verdict": "APPROVE", "versionNumber": nil}, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testhome.Temp(t)
			p := newPRServer(t, tc.viewerIsAuthor)
			req := map[string]any{"reviewId": p.review.ID, "versionNumber": 1}
			for k, v := range tc.req {
				req[k] = v
			}
			resp := postJSON(t, p.srv.URL+"/api/submit", req)
			if resp.StatusCode != tc.status {
				t.Fatalf("submit status = %d, want %d", resp.StatusCode, tc.status)
			}
			reqs := writesTo(p.gh, reviewsAt)
			if len(reqs) != len(tc.posted) {
				t.Fatalf("GitHub saw %d reviews, want %d", len(reqs), len(tc.posted))
			}
			for i, want := range tc.posted {
				got := reqs[i].Body
				if got["event"] != want["event"] || got["body"] != want["body"] || got["commit_id"] != prHeadSHA || reqs[i].Token != userToken {
					t.Fatalf("review %d = %v (token %q), want %v as the user", i, got, reqs[i].Token, want)
				}
			}
			sub, err := p.st.GetReview(context.Background(), p.review.ID)
			if err != nil {
				t.Fatal(err)
			}
			if wantStatus := map[bool]string{true: "submitted", false: "open"}[tc.status == http.StatusOK]; sub.Status != wantStatus {
				t.Fatalf("review status = %s, want %s", sub.Status, wantStatus)
			}
		})
	}
}

func TestPRSubmitPinsTheDisplayedHead(t *testing.T) {
	testhome.Temp(t)
	p := newPRServer(t, false)
	if _, _, err := p.st.CreateVersion(t.Context(), p.review.ID, "feature", "pushedsha", "",
		[]store.SectionInput{{Position: 0, Branch: "feature", ParentBranch: "main", BaseRef: "base7sha", HeadRef: "pushedsha", FilesJSON: "[]", PRNumber: prNumber}}); err != nil {
		t.Fatal(err)
	}
	resp := postJSON(t, p.srv.URL+"/api/submit", map[string]any{"reviewId": p.review.ID, "versionNumber": 1, "verdict": "APPROVE", "summary": "lgtm"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit status = %d, want 200", resp.StatusCode)
	}
	reqs := writesTo(p.gh, "/repos/o/r/pulls/7/reviews")
	if len(reqs) != 1 || reqs[0].Body["commit_id"] != prHeadSHA {
		t.Fatalf("reviews = %+v, want one pinned to the displayed head %s", reqs, prHeadSHA)
	}
}

func TestLocalSubmitRejectsVerdict(t *testing.T) {
	st, _, srv := newTestServer(t)
	review, _, _ := createReviewVersion(t, st, "[]")
	resp := postJSON(t, srv.URL+"/api/submit", map[string]any{"reviewId": review.ID, "verdict": "APPROVE"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSubmitCarriesSummaryToClaude(t *testing.T) {
	testhome.Temp(t)
	st, cc, srv := newTestServer(t)
	review, _, _ := createReviewVersion(t, st, "[]")
	resp := postJSON(t, srv.URL+"/api/submit", map[string]any{"reviewId": review.ID, "summary": "ship it after the rename"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	evs, err := cc.EventsSince(context.Background(), review.ID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var submit struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(evs[len(evs)-1].Payload, &submit); err != nil {
		t.Fatal(err)
	}
	if evs[len(evs)-1].Type != store.EventSubmit || submit.Summary != "ship it after the rename" {
		t.Fatalf("last event = %s %s, want submit carrying the summary", evs[len(evs)-1].Type, evs[len(evs)-1].Payload)
	}
}

func TestLocalCommentStaysLocal(t *testing.T) {
	st, _, srv := newTestServer(t)
	_, _, sec := createReviewVersion(t, st, "[]")
	resp := postJSON(t, srv.URL+"/api/comments", map[string]any{
		"sectionId": strconv.FormatInt(sec.ID, 10), "filePath": "a.go", "side": "additions",
		"range": map[string]any{"start": 1, "end": 1}, "body": "hm",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(out.ID, 10, 64)
	c, err := st.GetComment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if c.SyncState != store.SyncLocal || c.Subject != "line" {
		t.Fatalf("local comment = %+v, want sync_state local, subject line", c)
	}
}
