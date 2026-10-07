package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/github/githubtest"
	"github.com/yasyf/cc-review/internal/outbound"
	"github.com/yasyf/cc-review/internal/store"
)

const (
	appToken   = "app-token"
	commentsAt = "/repos/o/r/pulls/7/comments"
	installURL = "https://github.com/apps/cc-review-test/installations/new"
)

// seedPRComment seeds a PR review whose one comment already lives on GitHub as
// review comment 501 in thread T_1, and wires an outbound Syncer whose app
// client is app.
func seedPRComment(t *testing.T, s *Server, gh *githubtest.Server, app outbound.AppClient) (req Request, reviewID string, commentID int64) {
	t.Helper()
	ctx := context.Background()
	s.rv.outbound = outbound.New(s.cc.DB, s.appendEvent, gh.Client("user-token"), app)
	root := t.TempDir()
	r, err := s.createReview(ctx, "s1", 0, root, "feature", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetReviewKind(ctx, r.ID, store.ReviewKindPR, "o/r", 7); err != nil {
		t.Fatal(err)
	}
	v, sections, err := s.store.CreateVersion(ctx, r.ID, "feature", "head7sha", "",
		[]store.SectionInput{{Position: 0, Branch: "feature", ParentBranch: "main", BaseRef: "base7sha", HeadRef: "head7sha", FilesJSON: `[{"path":"a.go","status":"M","fingerprint":"fp","generated":false,"vendored":false}]`, PRNumber: 7}})
	if err != nil {
		t.Fatal(err)
	}
	cid, err := s.store.CreateComment(ctx, store.Comment{
		VersionID: v.ID, SectionID: sections[0].ID, Branch: sections[0].Key(),
		FilePath: "a.go", Side: "additions", StartLine: 2, EndLine: 2, Body: "why?",
		Author: store.AuthorRemote, SyncState: store.SyncSynced,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetCommentSync(ctx, cid, store.SyncSynced, "501", "T_1", "https://github.com/o/r/pull/7#discussion_r501", ""); err != nil {
		t.Fatal(err)
	}
	return Request{Session: "s1", Cwd: root}, r.ID, cid
}

func appClient(gh *githubtest.Server) outbound.AppClient {
	return func(context.Context, github.Repo) (*github.Client, error) { return gh.Client(appToken), nil }
}

func notInstalled(context.Context, github.Repo) (*github.Client, error) {
	return nil, fmt.Errorf("%w on o/r: install it at %s", ghapp.ErrNotInstalled, installURL)
}

func TestClaudeReplyPostsAsTheApp(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ReplyInput
		body string
	}{
		{
			name: "clarification",
			in:   ReplyInput{Kind: "clarification", Body: "It guards the retry path."},
			body: "It guards the retry path.",
		},
		{
			name: "ask renders options as a list",
			in: ReplyInput{Kind: "ask", Body: "Which fix?", Ask: &store.Ask{Header: "Fix", Options: []store.AskOption{
				{Label: "Inline it", Description: "one call site"}, {Label: "Keep the helper"},
			}}},
			body: "Which fix?\n\n**Fix**\n\n- **Inline it**: one call site\n- **Keep the helper**",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServer(t)
			gh := githubtest.New(t)
			_, reviewID, cid := seedPRComment(t, s, gh, appClient(gh))
			tc.in.CommentID = cid
			res := s.handleReply(t.Context(), Request{Replies: []ReplyInput{tc.in}})
			if !res.OK {
				t.Fatalf("reply failed: %s", res.Error)
			}
			reqs := gh.Requests(http.MethodPost, commentsAt+"/501/replies")
			if len(reqs) != 1 {
				t.Fatalf("GitHub saw %d replies, want 1", len(reqs))
			}
			if reqs[0].Authorization != "Bearer "+appToken {
				t.Fatalf("authorization = %q, want the app token", reqs[0].Authorization)
			}
			var posted struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal(reqs[0].Body, &posted); err != nil {
				t.Fatal(err)
			}
			if posted.Body != tc.body {
				t.Fatalf("posted body = %q, want %q", posted.Body, tc.body)
			}
			replies, err := s.store.ListRepliesByComment(t.Context(), cid)
			if err != nil {
				t.Fatal(err)
			}
			if len(replies) != 1 || replies[0].SyncState != store.SyncSynced || replies[0].RemoteID == "" {
				t.Fatalf("replies = %+v, want one synced reply", replies)
			}
			if n := countEvents(t, s, reviewID, store.EventCommentSynced); n != 1 {
				t.Fatalf("comment.synced events = %d, want 1", n)
			}
		})
	}
}

func TestClaudeReplyFailsCleanlyWithoutTheApp(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  outbound.AppClient
		want string
	}{
		{name: "not installed", app: notInstalled, want: installURL},
		{name: "not set up", app: func(context.Context, github.Repo) (*github.Client, error) { return nil, outbound.ErrNoApp }, want: "cc-review github setup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServer(t)
			gh := githubtest.New(t)
			_, _, cid := seedPRComment(t, s, gh, tc.app)
			res := s.handleReply(t.Context(), Request{Replies: []ReplyInput{{CommentID: cid, Kind: "clarification", Body: "hi"}}})
			if res.OK || !strings.Contains(res.Error, tc.want) {
				t.Fatalf("reply = ok %v, error %q; want an error naming %q", res.OK, res.Error, tc.want)
			}
			replies, err := s.store.ListRepliesByComment(t.Context(), cid)
			if err != nil {
				t.Fatal(err)
			}
			if len(replies) != 0 {
				t.Fatalf("replies = %+v, want none written", replies)
			}
			if reqs := gh.Requests(http.MethodPost, commentsAt+"/501/replies"); len(reqs) != 0 {
				t.Fatalf("GitHub saw %d replies, want 0", len(reqs))
			}
		})
	}
}

func TestClaudeReplyGitHubFailureIsReported(t *testing.T) {
	s, _ := testServer(t)
	gh := githubtest.New(t)
	_, _, cid := seedPRComment(t, s, gh, appClient(gh))
	gh.FailNext(http.MethodPost, commentsAt+"/501/replies", http.StatusUnprocessableEntity)
	res := s.handleReply(t.Context(), Request{Replies: []ReplyInput{{CommentID: cid, Kind: "clarification", Body: "hi"}}})
	if res.OK || !strings.Contains(res.Error, "422") {
		t.Fatalf("reply = ok %v, error %q; want the 422", res.OK, res.Error)
	}
	replies, err := s.store.ListRepliesByComment(t.Context(), cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].SyncState != store.SyncFailed || !strings.Contains(replies[0].SyncError, "422") {
		t.Fatalf("replies = %+v, want one failed reply", replies)
	}
}

func TestClaudeAnnotateCommentPostsAsTheApp(t *testing.T) {
	s, _ := testServer(t)
	gh := githubtest.New(t)
	req, reviewID, _ := seedPRComment(t, s, gh, appClient(gh))
	req.Annotations = []AnnotateInput{
		{Kind: "highlight", SectionKey: "feature", FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "new"},
		{Kind: "comment", SectionKey: "feature", FilePath: "a.go", Side: "additions", StartLine: 4, EndLine: 6, Body: "is this intended?"},
	}
	if res := s.handleAnnotate(t.Context(), req); !res.OK {
		t.Fatalf("annotate failed: %s", res.Error)
	}
	reqs := gh.Requests(http.MethodPost, commentsAt)
	if len(reqs) != 1 || reqs[0].Authorization != "Bearer "+appToken {
		t.Fatalf("comment posts = %+v, want one app-token post", reqs)
	}
	var posted map[string]any
	if err := json.Unmarshal(reqs[0].Body, &posted); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"commit_id": "head7sha", "path": "a.go", "body": "is this intended?", "line": float64(6), "side": "RIGHT", "start_line": float64(4), "start_side": "RIGHT"}
	if !reflect.DeepEqual(posted, want) {
		t.Fatalf("posted = %v, want %v", posted, want)
	}
	v, _, err := s.store.LatestVersion(t.Context(), reviewID)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := s.store.ListCommentsByVersion(t.Context(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 || comments[1].Author != store.AuthorClaude || comments[1].SyncState != store.SyncSynced || comments[1].RemoteID == "" {
		t.Fatalf("comments = %+v, want Claude's comment synced", comments)
	}
}

func TestClaudeAnnotateCommentFailsCleanlyWhenNotInstalled(t *testing.T) {
	s, _ := testServer(t)
	gh := githubtest.New(t)
	req, reviewID, _ := seedPRComment(t, s, gh, notInstalled)
	req.Annotations = []AnnotateInput{
		{Kind: "comment", SectionKey: "feature", FilePath: "a.go", Side: "additions", StartLine: 4, EndLine: 4, Body: "hm"},
	}
	res := s.handleAnnotate(t.Context(), req)
	if res.OK || !strings.Contains(res.Error, installURL) {
		t.Fatalf("annotate = ok %v, error %q; want the install URL", res.OK, res.Error)
	}
	v, _, err := s.store.LatestVersion(t.Context(), reviewID)
	if err != nil {
		t.Fatal(err)
	}
	comments, err := s.store.ListCommentsByVersion(t.Context(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want only the seeded one", len(comments))
	}
}
