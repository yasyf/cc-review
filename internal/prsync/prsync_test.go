package prsync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yasyf/cc-interact/consume"
	ccd "github.com/yasyf/cc-interact/daemon"
	ccevent "github.com/yasyf/cc-interact/event"
	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-interact/sse"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/ghapp/ghapptest"
	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/github/githubtest"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/testhome"
)

const (
	viewerToken = "viewer-token"
	viewerLogin = "octo"
	botLogin    = "cc-review-octo[bot]"
	coworker    = "hubot"
	headA       = "aaaa000000000000000000000000000000000000"
	headB       = "bbbb000000000000000000000000000000000000"
)

var testRepo = github.Repo{Owner: "octo", Name: "widgets"}

type fixture struct {
	t        *testing.T
	gh       *githubtest.Server
	cc       *ccstore.Store
	st       *store.Store
	bus      *ccevent.Bus
	activity *ccd.Activity
	syncer   *Syncer
	reviewID string
	requests atomic.Int64
	watched  atomic.Bool
	cursor   int64
}

type countingToken struct {
	token string
	n     *atomic.Int64
}

func (c countingToken) Token(context.Context) (string, error) {
	c.n.Add(1)
	return c.token, nil
}

func newFixture(t *testing.T, installApp bool) *fixture {
	t.Helper()
	testhome.Temp(t)
	if installApp {
		ghapptest.Install(t, ghapp.App{ID: 7, Slug: "cc-review-octo", BotLogin: botLogin, Owner: viewerLogin})
	}
	ctx := t.Context()
	cc, err := ccstore.Open(ctx, filepath.Join(t.TempDir(), "t.db"), store.Schema())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	f := &fixture{t: t, gh: githubtest.New(t), cc: cc, st: store.New(cc.DB()), bus: ccevent.NewBus(), activity: ccd.NewActivity()}
	f.gh.Login(viewerToken, viewerLogin)
	f.gh.SetDefaultBranch(testRepo, "main")
	f.gh.AddPR(testRepo, github.PullRequest{Number: 1, Title: "base", AuthorLogin: viewerLogin, HeadRefName: "feat-a", HeadRefOid: headA, BaseRefName: "main"})
	f.gh.AddPR(testRepo, github.PullRequest{Number: 2, Title: "top", AuthorLogin: coworker, HeadRefName: "feat-b", HeadRefOid: headB, BaseRefName: "feat-a"})

	sub, err := ccstore.NewSubjectStore(cc.DB()).Create(ctx, store.NewSlugHash(), store.ReviewSlug(store.NewSlugHash()), "s1", "/repo", 100, statusOpen)
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}
	f.reviewID = sub.ID
	if err := f.st.SetReviewMeta(ctx, f.reviewID, "", "feat-b", true); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetReviewKind(ctx, f.reviewID, store.ReviewKindPR, testRepo.String(), 2); err != nil {
		t.Fatal(err)
	}
	f.createVersion(headA, headB)

	client := github.New(countingToken{token: viewerToken, n: &f.requests}, github.WithBaseURL(f.gh.URL(), f.gh.URL()+"/graphql"))
	f.syncer = New(Config{
		DB:        cc.DB,
		Append:    f.append,
		Client:    client,
		Watched:   func(string) bool { return f.watched.Load() },
		Recapture: f.recapture,
		Background: func(fn func(context.Context)) {
			go fn(ctx)
		},
		Log: log.New(io.Discard, "", 0),
	})
	return f
}

func (f *fixture) createVersion(heads ...string) {
	f.t.Helper()
	inputs := make([]store.SectionInput, len(heads))
	for i, head := range heads {
		inputs[i] = store.SectionInput{
			Position: i, Branch: "feat-" + string(rune('a'+i)), ParentBranch: "main", BaseRef: "base", HeadRef: head,
			FilesJSON: "[]", PRNumber: i + 1,
		}
	}
	if _, _, err := f.st.CreateVersion(f.t.Context(), f.reviewID, "feat-b", "", "s1", inputs); err != nil {
		f.t.Fatalf("create version: %v", err)
	}
}

func (f *fixture) recapture(ctx context.Context, reviewID string) error {
	heads := []string{f.gh.Snapshot(testRepo, 1).PR.HeadRefOid, f.gh.Snapshot(testRepo, 2).PR.HeadRefOid}
	f.createVersion(heads...)
	return nil
}

func (f *fixture) append(ctx context.Context, e *ccevent.Event) (int64, error) {
	seq, err := f.cc.AppendEvent(ctx, e)
	if err != nil {
		return 0, err
	}
	f.bus.Publish(e.SubjectID)
	return seq, nil
}

func (f *fixture) poller() *poller {
	f.t.Helper()
	app, ok, err := ghapp.Load()
	if err != nil || !ok {
		f.t.Fatalf("load app: ok=%v err=%v", ok, err)
	}
	return &poller{reviewID: f.reviewID, app: app, viewer: viewerLogin}
}

func (f *fixture) poll() {
	f.t.Helper()
	live, err := f.syncer.poll(f.t.Context(), f.poller())
	if err != nil {
		f.t.Fatalf("poll: %v", err)
	}
	if !live {
		f.t.Fatal("poll reported the review is no longer syncable")
	}
}

type firedEvent struct {
	Type    string
	Origin  string
	Payload struct {
		CommentID   string `json:"commentId"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pullRequest"`
		Comment struct {
			Author  string `json:"author"`
			Status  string `json:"status"`
			Body    string `json:"body"`
			Replies []struct {
				Body string `json:"body"`
			} `json:"replies"`
		} `json:"comment"`
	}
}

// drain returns the events appended since the previous drain.
func (f *fixture) drain() []firedEvent {
	f.t.Helper()
	evs, err := f.cc.EventsSince(f.t.Context(), f.reviewID, f.cursor, "")
	if err != nil {
		f.t.Fatal(err)
	}
	out := make([]firedEvent, len(evs))
	for i, e := range evs {
		f.cursor = e.Seq
		out[i] = firedEvent{Type: e.Type, Origin: e.Origin}
		if err := json.Unmarshal(e.Payload, &out[i].Payload); err != nil {
			f.t.Fatal(err)
		}
	}
	return out
}

func (f *fixture) comment(remoteID string) store.Comment {
	f.t.Helper()
	c, err := f.st.CommentByRemoteID(f.t.Context(), f.reviewID, remoteID)
	if err != nil {
		f.t.Fatalf("comment %s: %v", remoteID, err)
	}
	return c
}

func types(evs []firedEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type + "/" + e.Origin
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPollMirrorsStackAndSecondPollIsSilent(t *testing.T) {
	f := newFixture(t, true)
	thread := f.gh.AddThread(testRepo, 2, github.Thread{
		Path: "main.go", SubjectType: "LINE", Line: 12, StartLine: 10, DiffSide: "RIGHT", StartDiffSide: "RIGHT",
		Comments: []github.RemoteComment{{AuthorLogin: coworker, Body: "rename this"}},
	})
	reply := f.gh.AddReply(testRepo, 2, thread.NodeID, github.RemoteComment{AuthorLogin: viewerLogin, Body: "will do"})
	outdated := f.gh.AddThread(testRepo, 1, github.Thread{
		Path: "old.go", SubjectType: "LINE", IsOutdated: true, OriginalLine: 4, DiffSide: "LEFT",
		Comments: []github.RemoteComment{{AuthorLogin: coworker, Body: "stale"}},
	})
	issue := f.gh.AddIssueComment(testRepo, 1, github.RemoteComment{AuthorLogin: coworker, Body: "LGTM overall"})

	f.poll()
	got := types(f.drain())
	want := []string{
		"pr.updated/system", "pr.updated/system",
		"comment.created/human", "comment.created/human", "comment.created/human",
	}
	if !equal(sortedPRFirst(got), want) {
		t.Fatalf("first poll events = %v, want %v", got, want)
	}

	v, ok, err := f.st.LatestVersion(t.Context(), f.reviewID)
	if err != nil || !ok {
		t.Fatalf("latest version: %v %v", ok, err)
	}
	sections, err := f.st.ListSections(t.Context(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := f.comment(thread.Comments[0].NodeID)
	wantLine := store.Comment{
		ID: line.ID, VersionID: v.ID, SectionID: sections[1].ID, Branch: "feat-b", FilePath: "main.go",
		Side: "additions", StartLine: 10, EndLine: 12, StartSide: "additions", EndSide: "additions",
		Body: "rename this", Author: store.AuthorRemote, Status: "open",
		RemoteID: thread.Comments[0].NodeID, RemoteThreadID: thread.NodeID, RemoteURL: thread.Comments[0].URL,
		AuthorLogin: coworker, AuthorAvatarURL: thread.Comments[0].AuthorAvatarURL,
		Subject: subjectLine, SyncState: store.SyncSynced, CreatedAt: line.CreatedAt, UpdatedAt: line.UpdatedAt,
	}
	if line != wantLine {
		t.Fatalf("line thread = %+v\nwant %+v", line, wantLine)
	}
	replies, err := f.st.ListRepliesByComment(t.Context(), line.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].RemoteID != reply.NodeID || replies[0].Origin != store.AuthorUser ||
		replies[0].Kind != replyKindNote || replies[0].Body != "will do" {
		t.Fatalf("replies = %+v", replies)
	}
	old := f.comment(outdated.Comments[0].NodeID)
	if old.Subject != subjectFile || !old.Outdated || old.StartLine != 4 || old.EndLine != 4 || old.Side != "deletions" ||
		old.SectionID != sections[0].ID {
		t.Fatalf("outdated thread = %+v", old)
	}
	conv := f.comment(issue.NodeID)
	if conv.FilePath != "" || conv.Subject != subjectFile || conv.RemoteThreadID != "" || conv.SectionID != sections[0].ID {
		t.Fatalf("issue comment = %+v", conv)
	}
	prs, err := f.st.PullRequests(t.Context(), f.reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || !prs[0].ViewerIsAuthor || prs[1].ViewerIsAuthor || prs[1].HeadSHA != headB {
		t.Fatalf("pull requests = %+v", prs)
	}

	f.poll()
	if evs := f.drain(); len(evs) != 0 {
		t.Fatalf("second poll emitted %v, want nothing", types(evs))
	}
}

func sortedPRFirst(in []string) []string {
	var prs, rest []string
	for _, s := range in {
		if s == "pr.updated/system" {
			prs = append(prs, s)
		} else {
			rest = append(rest, s)
		}
	}
	return append(prs, rest...)
}

func TestRemoteChangesEmitOncePerChange(t *testing.T) {
	f := newFixture(t, true)
	thread := f.gh.AddThread(testRepo, 2, github.Thread{
		Path: "main.go", SubjectType: "LINE", Line: 3, DiffSide: "RIGHT",
		Comments: []github.RemoteComment{{AuthorLogin: coworker, Body: "why?"}},
	})
	f.poll()
	f.drain()
	root := thread.Comments[0].NodeID

	steps := []struct {
		name   string
		act    func()
		want   []string
		status string
	}{
		{
			name:   "coworker resolves",
			act:    func() { f.gh.SetResolved(testRepo, 2, thread.NodeID, true) },
			want:   []string{"comment.resolved/human"},
			status: "resolved",
		},
		{
			name:   "coworker reopens",
			act:    func() { f.gh.SetResolved(testRepo, 2, thread.NodeID, false) },
			want:   []string{"comment.updated/human"},
			status: "open",
		},
		{
			name: "app bot replies",
			act: func() {
				f.gh.AddReply(testRepo, 2, thread.NodeID, github.RemoteComment{AuthorLogin: botLogin, Body: "because"})
			},
			want:   []string{"comment.updated/agent"},
			status: "open",
		},
		{
			name: "coworker replies",
			act: func() {
				f.gh.AddReply(testRepo, 2, thread.NodeID, github.RemoteComment{AuthorLogin: coworker, Body: "ok"})
			},
			want:   []string{"comment.updated/human"},
			status: "open",
		},
		{
			name:   "nothing changes",
			act:    func() {},
			want:   []string{},
			status: "open",
		},
	}
	for _, step := range steps {
		step.act()
		f.poll()
		if got := types(f.drain()); !equal(got, step.want) {
			t.Fatalf("%s: events = %v, want %v", step.name, got, step.want)
		}
		if c := f.comment(root); c.Status != step.status {
			t.Fatalf("%s: status = %q, want %q", step.name, c.Status, step.status)
		}
	}
	c := f.comment(root)
	replies, err := f.st.ListRepliesByComment(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 || replies[0].Origin != store.AuthorClaude || replies[1].Origin != store.AuthorRemote {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestHeadChangeRecapturesAndReanchors(t *testing.T) {
	f := newFixture(t, true)
	thread := f.gh.AddThread(testRepo, 2, github.Thread{
		Path: "main.go", SubjectType: "LINE", Line: 3, DiffSide: "RIGHT",
		Comments: []github.RemoteComment{{AuthorLogin: coworker, Body: "nit"}},
	})
	f.poll()
	f.drain()

	const moved = "cccc000000000000000000000000000000000000"
	f.gh.UpdatePR(testRepo, 2, func(pr *github.PullRequest) { pr.HeadRefOid = moved })
	f.poll()

	versions, err := f.st.ListVersions(t.Context(), f.reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	sections, err := f.st.ListSections(t.Context(), versions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if sections[1].HeadRef != moved {
		t.Fatalf("new head = %q, want %q", sections[1].HeadRef, moved)
	}
	if c := f.comment(thread.Comments[0].NodeID); c.VersionID != versions[1].ID || c.SectionID != sections[1].ID {
		t.Fatalf("thread stayed on version %d section %d, want %d/%d", c.VersionID, c.SectionID, versions[1].ID, sections[1].ID)
	}
	if got, want := types(f.drain()), []string{"pr.updated/system", "comment.updated/agent"}; !equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}

	f.poll()
	if versions, _ := f.st.ListVersions(t.Context(), f.reviewID); len(versions) != 2 {
		t.Fatalf("an unmoved head recaptured again: %d versions", len(versions))
	}
}

// sseBackend is the slice of the daemon the SSE plane reads through, over the
// fixture's store, bus, and presence registry.
type sseBackend struct{ f *fixture }

func (b sseBackend) ResolveSubject(_ context.Context, ref string) (string, bool, error) {
	return ref, ref == b.f.reviewID, nil
}

func (b sseBackend) EventsSince(ctx context.Context, subjectID string, cursor int64, excludeOrigin string) ([]ccevent.Event, error) {
	return b.f.cc.EventsSince(ctx, subjectID, cursor, excludeOrigin)
}

func (b sseBackend) Subscribe(subjectID string) (<-chan struct{}, func()) {
	return b.f.bus.Subscribe(subjectID)
}

func (b sseBackend) Attach(subjectID, consumer string, pid int) func() {
	return b.f.activity.Attach(subjectID, consumer, pid)
}

func (b sseBackend) ConsumerConnected(subjectID string) bool {
	return b.f.activity.AttachedWithin(subjectID, 0)
}

func (b sseBackend) AttachViewer(subjectID string) func() {
	return b.f.activity.AttachViewer(subjectID)
}

func TestBotCommentNeverReachesTheChannel(t *testing.T) {
	f := newFixture(t, true)
	bot := f.gh.AddThread(testRepo, 2, github.Thread{
		Path: "main.go", SubjectType: "LINE", Line: 3, DiffSide: "RIGHT",
		Comments: []github.RemoteComment{{AuthorLogin: botLogin, Body: "from claude"}},
	})
	human := f.gh.AddThread(testRepo, 2, github.Thread{
		Path: "main.go", SubjectType: "LINE", Line: 9, DiffSide: "RIGHT",
		Comments: []github.RemoteComment{{AuthorLogin: coworker, Body: "from a coworker"}},
	})
	f.poll()
	if c := f.comment(bot.Comments[0].NodeID); c.Author != store.AuthorClaude || c.AuthorLogin != botLogin {
		t.Fatalf("bot comment = %+v", c)
	}
	const sentinel = "test.done"
	f.append(t.Context(), &ccevent.Event{SubjectID: f.reviewID, Origin: ccevent.OriginHuman, Type: sentinel, Payload: []byte(`{"type":"test.done"}`)})

	srv := httptest.NewServer(sse.NewServer(sseBackend{f}, sse.Config{}).Handler())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var (
		mu       sync.Mutex
		received []string
	)
	err = consume.ConsumeEvents(ctx, consume.StreamSource{
		Port: port, SubjectID: f.reviewID, Consumer: "channel", ExcludeOrigin: ccevent.OriginAgent, Paths: paths.App(),
	}, func(_ int64, data string) (bool, error) {
		var e struct {
			Type    string `json:"type"`
			Comment struct {
				Body string `json:"body"`
			} `json:"comment"`
		}
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return true, err
		}
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e.Type+":"+e.Comment.Body)
		return e.Type == sentinel, nil
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	want := []string{"pr.updated:", "pr.updated:", "comment.created:" + human.Comments[0].Body, sentinel + ":"}
	if !equal(sortedConsumed(received), want) {
		t.Fatalf("channel received %v, want %v", received, want)
	}
}

func sortedConsumed(in []string) []string {
	var prs, rest []string
	for _, s := range in {
		if s == "pr.updated:" {
			prs = append(prs, s)
		} else {
			rest = append(rest, s)
		}
	}
	return append(prs, rest...)
}

func TestPollerIntervalFollowsWatched(t *testing.T) {
	watchedInterval, idleInterval = 5*time.Millisecond, time.Hour
	t.Cleanup(func() { watchedInterval, idleInterval = 15*time.Second, 2*time.Minute })
	f := newFixture(t, true)

	if err := f.syncer.Start(t.Context(), f.reviewID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := f.syncer.Start(t.Context(), f.reviewID); err != nil {
		t.Fatalf("second start: %v", err)
	}
	waitFor(t, "first poll", func() bool { return len(f.drain()) > 0 })
	idle := f.requests.Load()
	time.Sleep(60 * time.Millisecond)
	if n := f.requests.Load(); n != idle {
		t.Fatalf("unwatched poller made %d requests inside the idle interval", n-idle)
	}

	f.watched.Store(true)
	waitFor(t, "watched polls", func() bool { return f.requests.Load() >= idle+3 })

	f.watched.Store(false)
	time.Sleep(20 * time.Millisecond)
	settled := f.requests.Load()
	time.Sleep(60 * time.Millisecond)
	if n := f.requests.Load(); n != settled {
		t.Fatalf("poller kept the watched interval after the viewer left: %d extra requests", n-settled)
	}

	f.syncer.Stop(f.reviewID)
	if f.syncer.Running(f.reviewID) {
		t.Fatal("poller still registered after Stop")
	}
}

func TestPollerExitsWhenReviewCloses(t *testing.T) {
	watchedInterval = 5 * time.Millisecond
	t.Cleanup(func() { watchedInterval = 15 * time.Second })
	f := newFixture(t, true)
	f.watched.Store(true)
	if err := f.syncer.Start(t.Context(), f.reviewID); err != nil {
		t.Fatal(err)
	}
	if err := ccstore.NewSubjectStore(f.cc.DB()).SetStatus(t.Context(), f.reviewID, "closed"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "poller exit", func() bool { return !f.syncer.Running(f.reviewID) })
}

func TestStartRefusesWithoutApp(t *testing.T) {
	f := newFixture(t, false)
	if err := f.syncer.Start(t.Context(), f.reviewID); !errors.Is(err, ErrAppMissing) {
		t.Fatalf("start = %v, want ErrAppMissing", err)
	}
	if f.syncer.Running(f.reviewID) {
		t.Fatal("poller running without an app")
	}
	if n := f.requests.Load(); n != 0 {
		t.Fatalf("refused start still made %d GitHub requests", n)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
