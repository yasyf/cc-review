package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/outbound"
	"github.com/yasyf/cc-review/internal/store"
)

var prRepoRef = github.Repo{Owner: "o", Name: "r"}

func noApp(context.Context, github.Repo) (*github.Client, string, error) {
	return nil, "", errors.New("no app in rest tests")
}

func (p *prServer) seedComment(t *testing.T, body, state string) int64 {
	t.Helper()
	id, err := p.st.CreateComment(t.Context(), store.Comment{
		VersionID: p.sec.VersionID, SectionID: p.sec.ID, Branch: p.sec.Key(), FilePath: "a.go", Side: "additions",
		StartLine: 5, EndLine: 5, Body: body, Author: store.AuthorUser, Status: "open", SyncState: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (p *prServer) mirror(t *testing.T, id int64, th github.Thread) {
	t.Helper()
	if err := p.st.SetCommentSync(t.Context(), id, store.SyncSynced, th.Comments[0].NodeID, th.NodeID, th.Comments[0].URL, ""); err != nil {
		t.Fatal(err)
	}
}

func (p *prServer) thread(author, body string) github.Thread {
	return p.gh.AddThread(prRepoRef, prNumber, github.Thread{
		Path: "a.go", SubjectType: "LINE", Line: 5, OriginalLine: 5, DiffSide: "RIGHT",
		Comments: []github.RemoteComment{{AuthorLogin: author, Body: body}},
	})
}

func TestOutboundStopSettlesInFlightWrites(t *testing.T) {
	p := newPRServer(t, false)
	entered, gate := make(chan struct{}, 1), make(chan struct{})
	syncer := outbound.New(p.cc.DB, p.cc.AppendEvent, p.gh.Client(userToken), noApp, func(string) func() {
		entered <- struct{}{}
		<-gate
		return func() {}
	})
	ctx, cancel := context.WithCancel(t.Context())
	if err := syncer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	id := p.seedComment(t, "nit", store.SyncPosting)
	syncer.PostComment(p.review.ID, id)
	<-entered
	cancel()

	stopped := make(chan struct{})
	go func() {
		syncer.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a write was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(gate)
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop never returned after the running write settled")
	}

	if n := len(writesTo(p.gh, commentsAt)); n != 0 {
		t.Fatalf("GitHub saw %d posts after shutdown cancelled the write, want 0", n)
	}
	if c := p.getComment(t, id); c.SyncState != store.SyncPosting {
		t.Fatalf("interrupted comment = %+v, want it left posting for the next Start", c)
	}
	if err := syncer.Serialize(t.Context(), p.review.ID, func(context.Context) error { return nil }); !errors.Is(err, outbound.ErrStopped) {
		t.Fatalf("Serialize after Stop = %v, want ErrStopped", err)
	}
}

func TestOutboundStartReconcilesInterruptedWrites(t *testing.T) {
	p := newPRServer(t, false)
	claimedThread := p.thread(viewer, "already posted")
	p.mirror(t, p.seedComment(t, "already posted", store.SyncSynced), claimedThread)
	p.thread("carol", "already posted")
	p.gh.AddThread(prRepoRef, prNumber, github.Thread{
		Path: "a.go", SubjectType: "LINE", Line: 9, OriginalLine: 9, DiffSide: "RIGHT",
		Comments: []github.RemoteComment{{AuthorLogin: viewer, Body: "already posted"}},
	})
	landed := p.thread(viewer, "already posted")
	adopt := p.seedComment(t, "already posted", store.SyncPosting)
	fresh := p.seedComment(t, "never posted", store.SyncPosting)

	replyParent := p.seedComment(t, "parent", store.SyncSynced)
	parentThread := p.thread(viewer, "parent")
	p.mirror(t, replyParent, parentThread)
	landedReply := p.gh.AddReply(prRepoRef, prNumber, parentThread.NodeID, github.RemoteComment{AuthorLogin: viewer, Body: "done"})
	replyID, _, err := p.st.CreateReply(t.Context(), store.Reply{CommentID: replyParent, Origin: store.OriginUser, Kind: "note", Body: "done", SyncState: store.SyncPosting})
	if err != nil {
		t.Fatal(err)
	}

	edited := p.seedComment(t, "old body", store.SyncPosting)
	p.mirror(t, edited, p.thread(viewer, "old body"))
	if err := p.st.UpdateCommentBody(t.Context(), edited, "new body"); err != nil {
		t.Fatal(err)
	}

	restarted := outbound.New(p.cc.DB, p.cc.AppendEvent, p.gh.Client(userToken), noApp, p.inbound.exclusive)
	if err := restarted.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Stop)
	if err := restarted.Serialize(t.Context(), p.review.ID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}

	if c := p.getComment(t, adopt); c.SyncState != store.SyncSynced || c.RemoteID != landed.Comments[0].NodeID || c.RemoteThreadID != landed.NodeID {
		t.Fatalf("interrupted post = %+v, want it to adopt %s in %s", c, landed.Comments[0].NodeID, landed.NodeID)
	}
	posts := writesTo(p.gh, commentsAt)
	if len(posts) != 1 || posts[0].Body["body"] != "never posted" {
		t.Fatalf("comment posts = %+v, want only the comment GitHub never saw", posts)
	}
	if c := p.getComment(t, fresh); c.SyncState != store.SyncSynced || c.RemoteID == "" {
		t.Fatalf("unposted comment = %+v, want it posted and synced", c)
	}
	r, err := p.st.GetReply(t.Context(), replyID)
	if err != nil {
		t.Fatal(err)
	}
	if r.SyncState != store.SyncSynced || r.RemoteID != landedReply.NodeID {
		t.Fatalf("interrupted reply = %+v, want it to adopt %s", r, landedReply.NodeID)
	}
	if n := len(writesTo(p.gh, replyOp)); n != 0 {
		t.Fatalf("GitHub saw %d replies, want the landed reply adopted", n)
	}
	if c := p.getComment(t, edited); c.SyncState != store.SyncSynced {
		t.Fatalf("interrupted edit = %+v, want synced", c)
	}
	updates := writesTo(p.gh, "graphql:UpdateReviewComment")
	if len(updates) != 1 || updates[0].Body["body"] != "new body" {
		t.Fatalf("comment updates = %+v, want the interrupted edit pushed once", updates)
	}
}

func TestPRCommentKeepsItsIdentityWhenThreadLookupFails(t *testing.T) {
	p := newPRServer(t, false)
	p.gh.FailNext("graphql:ThreadOfComment", http.StatusBadGateway, "Bad Gateway")
	id := p.comment(t, nil)
	p.settle(t)

	c := p.getComment(t, id)
	if c.SyncState != store.SyncFailed || c.RemoteID == "" || c.RemoteURL == "" || c.RemoteThreadID != "" {
		t.Fatalf("comment after the thread lookup failed = %+v, want failed with the created comment's identity", c)
	}

	resp := postJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10)+"/retry", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", resp.StatusCode)
	}
	p.settle(t)
	threads := p.gh.Snapshot(prRepoRef, prNumber).Threads
	if c = p.getComment(t, id); c.SyncState != store.SyncSynced || len(threads) != 1 || c.RemoteThreadID != threads[0].NodeID {
		t.Fatalf("comment after retry = %+v, want synced on thread %+v", c, threads)
	}
	if n := len(writesTo(p.gh, commentsAt)); n != 1 {
		t.Fatalf("GitHub saw %d comment posts, want the retry to find the thread instead of posting again", n)
	}
}

func (p *prServer) retry(t *testing.T, commentID int64, body map[string]any) {
	t.Helper()
	resp := postJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(commentID, 10)+"/retry", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", resp.StatusCode)
	}
	p.settle(t)
}

func TestPRRetryAdoptsACommentWhosePostResponseWasLost(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		req    map[string]any
		remote func(github.PRSnapshot) []github.RemoteComment
	}{
		{
			name: "line comment", path: commentsAt,
			remote: func(s github.PRSnapshot) []github.RemoteComment {
				var out []github.RemoteComment
				for _, th := range s.Threads {
					out = append(out, th.Comments...)
				}
				return out
			},
		},
		{
			name: "conversation comment", path: issuesAt,
			req:    map[string]any{"subject": "file", "filePath": "", "range": map[string]any{"start": 0, "end": 0}},
			remote: func(s github.PRSnapshot) []github.RemoteComment { return s.IssueComments },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPRServer(t, false)
			p.gh.LoseNext(tc.path)
			id := p.comment(t, tc.req)
			p.settle(t)
			if c := p.getComment(t, id); c.SyncState != store.SyncFailed || c.RemoteID != "" {
				t.Fatalf("comment after the lost response = %+v, want failed with no remote id", c)
			}

			p.retry(t, id, map[string]any{})
			remote := tc.remote(p.gh.Snapshot(prRepoRef, prNumber))
			if len(remote) != 1 {
				t.Fatalf("GitHub holds %d comments, want exactly the one the lost post created", len(remote))
			}
			if c := p.getComment(t, id); c.SyncState != store.SyncSynced || c.RemoteID != remote[0].NodeID {
				t.Fatalf("comment after retry = %+v, want synced as %s", c, remote[0].NodeID)
			}
			if n := len(writesTo(p.gh, tc.path)); n != 1 {
				t.Fatalf("GitHub saw %d posts, want the retry to adopt instead of posting", n)
			}
		})
	}
}

func TestPRRetryAdoptsAReplyWhosePostResponseWasLost(t *testing.T) {
	p := newPRServer(t, false)
	id := p.comment(t, nil)
	p.settle(t)
	p.gh.LoseNext(replyOp)
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
	if r, err := p.st.GetReply(t.Context(), replyID); err != nil || r.SyncState != store.SyncFailed || r.RemoteID != "" {
		t.Fatalf("reply after the lost response = %+v, %v; want failed with no remote id", r, err)
	}

	p.retry(t, id, map[string]any{"replyId": out.ID})
	threads := p.gh.Snapshot(prRepoRef, prNumber).Threads
	if len(threads) != 1 || len(threads[0].Comments) != 2 {
		t.Fatalf("threads = %+v, want one thread holding the comment and exactly one reply", threads)
	}
	r, err := p.st.GetReply(t.Context(), replyID)
	if err != nil {
		t.Fatal(err)
	}
	if r.SyncState != store.SyncSynced || r.RemoteID != threads[0].Comments[1].NodeID {
		t.Fatalf("reply after retry = %+v, want synced as %s", r, threads[0].Comments[1].NodeID)
	}
	if n := len(writesTo(p.gh, replyOp)); n != 1 {
		t.Fatalf("GitHub saw %d replies, want the retry to adopt instead of replying again", n)
	}
}

func TestQueuedPostSkipsARowThePollerAlreadyAdopted(t *testing.T) {
	p := newPRServer(t, false)
	th := p.thread(viewer, "nit")
	id := p.seedComment(t, "nit", store.SyncPosting)
	p.mirror(t, id, th)
	parent := p.seedComment(t, "parent", store.SyncSynced)
	parentThread := p.thread(viewer, "parent")
	p.mirror(t, parent, parentThread)
	reply := p.gh.AddReply(prRepoRef, prNumber, parentThread.NodeID, github.RemoteComment{AuthorLogin: viewer, Body: "done"})
	replyID, _, err := p.st.CreateReply(t.Context(), store.Reply{CommentID: parent, Origin: store.OriginUser, Kind: "note", Body: "done", SyncState: store.SyncPosting})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.st.SetReplySync(t.Context(), replyID, store.SyncSynced, reply.NodeID, reply.URL, ""); err != nil {
		t.Fatal(err)
	}

	p.sync.PostComment(p.review.ID, id)
	p.sync.PostReply(p.review.ID, replyID)
	p.settle(t)

	if n := len(writesTo(p.gh, commentsAt)); n != 0 {
		t.Fatalf("GitHub saw %d comment posts, want none for a row that already mirrors a GitHub comment", n)
	}
	if n := len(writesTo(p.gh, replyOp)); n != 0 {
		t.Fatalf("GitHub saw %d replies, want none for a reply that already mirrors a GitHub comment", n)
	}
}

func TestPRTwoQueuedEditsSettleOnlyOnTheNewest(t *testing.T) {
	p := newPRServer(t, false)
	id := p.comment(t, nil)
	p.settle(t)

	release := p.inbound.exclusive(p.review.ID)
	for _, body := range []string{"first", "second"} {
		if resp := putJSON(t, p.srv.URL+"/api/comments/"+strconv.FormatInt(id, 10), map[string]any{"body": body}); resp.StatusCode != http.StatusOK {
			t.Fatalf("edit %q status = %d, want 200", body, resp.StatusCode)
		}
	}
	release()
	p.settle(t)

	edits := writesTo(p.gh, "graphql:UpdateReviewComment")
	if len(edits) != 2 || edits[0].Body["body"] != "first" || edits[1].Body["body"] != "second" {
		t.Fatalf("edits = %+v, want first then second", edits)
	}
	if c := p.getComment(t, id); c.SyncState != store.SyncSynced || c.Body != "second" {
		t.Fatalf("comment = %+v, want synced on the second body", c)
	}
	want := []string{store.SyncSynced, store.SyncPosting, store.SyncPosting, store.SyncPosting, store.SyncSynced}
	if got := syncedStates(t, p.cc, p.review.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("comment.synced states = %v, want %v: the first edit's result must not settle a row the second edit still owns", got, want)
	}
}
