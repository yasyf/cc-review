package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSchemaFingerprintPinned(t *testing.T) {
	got, err := Schema().Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	const want = "f093bc74c403b1561b9f22f83de36aafcff8c57963f015a855571f1d850eb333"
	if got != want {
		t.Fatalf("Schema().Fingerprint() = %q, want %q; a DDL change is a schema epoch: update the pin and the CHANGELOG", got, want)
	}
}

func seedPRVersion(ctx context.Context, t *testing.T, s *Store) (string, Version, Section) {
	t.Helper()
	reviewID := seedReview(ctx, t, s, "s", 0, "/repo", "feat", "base0")
	v, sections, err := s.CreateVersion(ctx, reviewID, "feat", "", "sess", []SectionInput{
		{Position: 0, Branch: "feat", ParentBranch: "main", BaseRef: "b0", HeadRef: "h0", FilesJSON: "[]", PRNumber: 7, PRNodeID: "PR_7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reviewID, v, sections[0]
}

func TestSectionsCarryPR(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, v, created := seedPRVersion(ctx, t, s)
	if created.PRNumber != 7 || created.PRNodeID != "PR_7" {
		t.Fatalf("created section = %+v, want PR #7 PR_7", created)
	}
	listed, err := s.ListSections(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].PRNumber != 7 || listed[0].PRNodeID != "PR_7" {
		t.Fatalf("ListSections = %+v, want one section with PR #7 PR_7", listed)
	}
}

func TestSetReviewKind(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID := seedReview(ctx, t, s, "s", 0, "/repo", "feat", "base0")
	meta, ok, err := s.GetReviewMeta(ctx, reviewID)
	if err != nil || !ok {
		t.Fatalf("GetReviewMeta = %v %v", ok, err)
	}
	if meta != (ReviewMeta{BaseRef: "base0", Branch: "feat", Kind: ReviewKindLocal}) {
		t.Fatalf("default meta = %+v", meta)
	}
	if err := s.SetReviewKind(ctx, reviewID, ReviewKindPR, "yasyf/cc-review", 42); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReviewMeta(ctx, reviewID, "", "feat", true); err != nil {
		t.Fatal(err)
	}
	meta, _, err = s.GetReviewMeta(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	want := ReviewMeta{Branch: "feat", Stack: true, Kind: ReviewKindPR, Repo: "yasyf/cc-review", PRNumber: 42}
	if meta != want {
		t.Fatalf("meta = %+v, want %+v", meta, want)
	}

	fresh, err := newSubjectStoreForTest(s).Create(ctx, NewSlugHash(), ReviewSlug(NewSlugHash()), "s2", "/repo", 0, "open")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetReviewKind(ctx, fresh.ID, ReviewKindPR, "o/r", 3); err != nil {
		t.Fatal(err)
	}
	meta, ok, err = s.GetReviewMeta(ctx, fresh.ID)
	if err != nil || !ok || meta != (ReviewMeta{Kind: ReviewKindPR, Repo: "o/r", PRNumber: 3}) {
		t.Fatalf("kind-only meta = %+v %v %v", meta, ok, err)
	}
	missing, ok, err := s.GetReviewMeta(ctx, "absent")
	if err != nil || ok || missing != (ReviewMeta{Kind: ReviewKindLocal}) {
		t.Fatalf("missing meta = %+v %v %v", missing, ok, err)
	}
}

func TestCreateCommentCarriesSyncFields(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, v, sec := seedPRVersion(ctx, t, s)
	localID, err := s.CreateComment(ctx, Comment{VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "local"})
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.GetComment(ctx, localID)
	if err != nil {
		t.Fatal(err)
	}
	if local.Author != AuthorUser || local.Subject != "line" || local.SyncState != SyncLocal || local.RemoteID != "" || local.Outdated {
		t.Fatalf("local comment defaults = %+v", local)
	}
	postingID, err := s.CreateComment(ctx, Comment{
		VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", StartLine: 0, EndLine: 0, Body: "file note",
		Subject: "file", SyncState: SyncPosting, AuthorLogin: "me", AuthorAvatarURL: "https://avatars/me",
	})
	if err != nil {
		t.Fatal(err)
	}
	posting, err := s.GetComment(ctx, postingID)
	if err != nil {
		t.Fatal(err)
	}
	if posting.Subject != "file" || posting.SyncState != SyncPosting || posting.AuthorLogin != "me" || posting.AuthorAvatarURL != "https://avatars/me" {
		t.Fatalf("posting comment = %+v", posting)
	}
}

func TestSetCommentSync(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID, v, sec := seedPRVersion(ctx, t, s)
	id, err := s.CreateComment(ctx, Comment{VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", StartLine: 2, EndLine: 3, Body: "b", SyncState: SyncPosting})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommentSync(ctx, id, SyncSynced, "PRRC_1", "PRRT_1", "https://gh/c/1", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommentSync(ctx, id, SyncFailed, "", "", "", "422 stale line"); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetComment(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.SyncState != SyncFailed || c.SyncError != "422 stale line" || c.RemoteID != "PRRC_1" || c.RemoteThreadID != "PRRT_1" || c.RemoteURL != "https://gh/c/1" {
		t.Fatalf("after failed sync = %+v, want failed with the earlier remote link kept", c)
	}
	byRemote, err := s.CommentByRemoteID(ctx, reviewID, "PRRC_1")
	if err != nil || byRemote.ID != id {
		t.Fatalf("CommentByRemoteID = %+v %v, want id %d", byRemote, err, id)
	}
	if _, err := s.CommentByRemoteID(ctx, reviewID, "PRRC_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CommentByRemoteID(missing) = %v, want ErrNotFound", err)
	}
	if err := s.SetCommentSync(ctx, 9999, SyncSynced, "", "", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetCommentSync(missing) = %v, want ErrNotFound", err)
	}
}

func TestUpsertRemoteCommentIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, v, sec := seedPRVersion(ctx, t, s)
	in := Comment{
		VersionID: v.ID, SectionID: sec.ID, Branch: "feat", FilePath: "a.go", Side: "additions", StartLine: 4, EndLine: 4,
		Body: "nit", Author: AuthorRemote, RemoteID: "PRRC_9", RemoteThreadID: "PRRT_9", RemoteURL: "https://gh/c/9",
		AuthorLogin: "octo", AuthorAvatarURL: "https://avatars/octo",
	}
	first, created, err := s.UpsertRemoteComment(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.ID == 0 || first.SyncState != SyncSynced || first.Author != AuthorRemote || first.Status != "open" ||
		first.Subject != "line" || first.AuthorLogin != "octo" || first.RemoteThreadID != "PRRT_9" {
		t.Fatalf("first upsert = %+v created=%v", first, created)
	}

	in.Body, in.Status, in.Outdated, in.StartLine, in.EndLine = "nit (edited)", "resolved", true, 0, 0
	second, created, err := s.UpsertRemoteComment(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("second upsert created=%v id=%d, want update of %d", created, second.ID, first.ID)
	}
	if second.Body != "nit (edited)" || second.Status != "resolved" || !second.Outdated || second.StartLine != 0 || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("second upsert = %+v", second)
	}
	all, err := s.ListCommentsByVersion(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("comments = %d, want 1", len(all))
	}
	if _, _, err := s.UpsertRemoteComment(ctx, Comment{VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions"}); err == nil {
		t.Fatal("UpsertRemoteComment accepted an empty remote id")
	}
}

func TestRemoteIdentityIsPerReview(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviews := make([]string, 2)
	versions := make([]Version, 2)
	sections := make([]Section, 2)
	for i, session := range []string{"s1", "s2"} {
		reviews[i] = seedReview(ctx, t, s, session, 0, "/repo", "feat", "base0")
		v, secs, err := s.CreateVersion(ctx, reviews[i], "feat", "", session, []SectionInput{
			{Position: 0, Branch: "feat", ParentBranch: "main", BaseRef: "b0", HeadRef: "h0", FilesJSON: "[]", PRNumber: 7, PRNodeID: "PR_7"},
		})
		if err != nil {
			t.Fatal(err)
		}
		versions[i], sections[i] = v, secs[0]
	}
	upsert := func(i int, body string) (Comment, Reply) {
		t.Helper()
		c, _, err := s.UpsertRemoteComment(ctx, Comment{
			VersionID: versions[i].ID, SectionID: sections[i].ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1,
			Body: body, Author: AuthorRemote, RemoteID: "PRRC_1", RemoteThreadID: "PRRT_1",
		})
		if err != nil {
			t.Fatal(err)
		}
		r, _, err := s.UpsertRemoteReply(ctx, Reply{CommentID: c.ID, Origin: AuthorRemote, Kind: "note", Body: body + " reply", RemoteID: "PRRC_2"})
		if err != nil {
			t.Fatal(err)
		}
		return c, r
	}
	upsert(0, "first")
	upsert(1, "first")
	upsert(0, "second")
	upsert(1, "second")
	for i, reviewID := range reviews {
		comments, err := s.ListCommentsByVersion(ctx, versions[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(comments) != 1 || comments[0].SectionID != sections[i].ID || comments[0].Body != "second" {
			t.Fatalf("review %d comments = %+v, want one on section %d", i, comments, sections[i].ID)
		}
		byRemote, err := s.CommentByRemoteID(ctx, reviewID, "PRRC_1")
		if err != nil || byRemote.ID != comments[0].ID {
			t.Fatalf("review %d CommentByRemoteID = %+v %v, want id %d", i, byRemote, err, comments[0].ID)
		}
		replies, err := s.ListRepliesByComment(ctx, comments[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(replies) != 1 || replies[0].Body != "second reply" {
			t.Fatalf("review %d replies = %+v, want its own reply", i, replies)
		}
	}
}

func TestUpsertRemoteCommentKeepsUnacknowledgedEdits(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, v, sec := seedPRVersion(ctx, t, s)
	in := Comment{
		VersionID: v.ID, SectionID: sec.ID, Branch: "feat", FilePath: "a.go", Side: "additions", StartLine: 4, EndLine: 4,
		Body: "remote", Author: AuthorUser, RemoteID: "PRRC_9", RemoteThreadID: "PRRT_9",
	}
	first, _, err := s.UpsertRemoteComment(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func() error{
		func() error { return s.UpdateCommentBody(ctx, first.ID, "local") },
		func() error { return s.UpdateCommentStatus(ctx, first.ID, "resolved") },
	} {
		if err := s.SetCommentSync(ctx, first.ID, SyncSynced, "", "", "", ""); err != nil {
			t.Fatal(err)
		}
		if err := edit(); err != nil {
			t.Fatal(err)
		}
		if c, err := s.GetComment(ctx, first.ID); err != nil || c.SyncState != SyncPosting {
			t.Fatalf("after a local edit = %+v %v, want posting", c, err)
		}
	}
	for _, state := range []string{SyncPosting, SyncFailed} {
		if err := s.SetCommentSync(ctx, first.ID, state, "", "", "", state); err != nil {
			t.Fatal(err)
		}
		in.EndLine = 9
		got, _, err := s.UpsertRemoteComment(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if got.Body != "local" || got.Status != "resolved" || got.SyncState != state || got.SyncError != state || got.EndLine != 9 {
			t.Fatalf("%s upsert = %+v, want the local body, status, and sync state kept with the new anchor", state, got)
		}
	}
	if err := s.SetCommentSync(ctx, first.ID, SyncSynced, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.UpsertRemoteComment(ctx, in); err != nil || got.Body != "remote" || got.Status != "open" {
		t.Fatalf("synced upsert = %+v %v, want GitHub's body and status", got, err)
	}
}

func TestConversationResolveStaysSynced(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, v, sec := seedPRVersion(ctx, t, s)
	c, _, err := s.UpsertRemoteComment(ctx, Comment{
		VersionID: v.ID, SectionID: sec.ID, Side: "additions", Subject: "file", Body: "LGTM", Author: AuthorRemote, RemoteID: "IC_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCommentStatus(ctx, c.ID, "resolved"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetComment(ctx, c.ID); err != nil || got.SyncState != SyncSynced || got.Status != "resolved" {
		t.Fatalf("resolved conversation comment = %+v %v, want resolved and still synced", got, err)
	}
}

func TestPendingEventsCommitWithTheirRows(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID, v, sec := seedPRVersion(ctx, t, s)
	in := Comment{VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", Body: "x", RemoteID: "PRRC_1"}
	boom := errors.New("boom")
	if err := s.ApplyRemote(ctx, func(rt *RemoteTx) error {
		if _, _, err := rt.UpsertComment(ctx, in); err != nil {
			return err
		}
		if err := rt.QueueEvent(ctx, reviewID, "human", "comment.created", []byte(`{}`)); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("ApplyRemote = %v, want boom", err)
	}
	if _, err := s.CommentByRemoteID(ctx, reviewID, "PRRC_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back comment = %v, want ErrNotFound", err)
	}
	if pending, err := s.PendingEvents(ctx, reviewID); err != nil || len(pending) != 0 {
		t.Fatalf("rolled-back pending events = %+v %v, want none", pending, err)
	}

	if err := s.ApplyRemote(ctx, func(rt *RemoteTx) error {
		if _, _, err := rt.UpsertComment(ctx, in); err != nil {
			return err
		}
		return rt.QueueEvent(ctx, reviewID, "human", "comment.created", []byte(`{"a":1}`))
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingEvents(ctx, reviewID)
	if err != nil || len(pending) != 1 || pending[0].Type != "comment.created" || string(pending[0].Payload) != `{"a":1}` {
		t.Fatalf("pending events = %+v %v", pending, err)
	}
	if err := s.MarkEventAppended(ctx, pending[0].ID, 7); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingEvents(ctx, reviewID); err != nil || len(pending) != 0 {
		t.Fatalf("pending after append = %+v %v, want none", pending, err)
	}
}

func TestAckCommentSyncSettlesOnlyTheNewestEdit(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, v, sec := seedPRVersion(ctx, t, s)
	c, _, err := s.UpsertRemoteComment(ctx, Comment{
		VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", Body: "a", RemoteID: "PRRC_1", RemoteThreadID: "PRRT_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"b", "c"} {
		if err := s.UpdateCommentBody(ctx, c.ID, body); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		seq   int64
		state string
		want  string
	}{
		{seq: 1, state: SyncSynced, want: SyncPosting},
		{seq: 1, state: SyncFailed, want: SyncPosting},
		{seq: 2, state: SyncFailed, want: SyncFailed},
		{seq: 2, state: SyncSynced, want: SyncSynced},
	}
	for _, step := range steps {
		if err := s.AckCommentSync(ctx, c.ID, step.seq, step.state, "", "", "https://gh/c/1", step.state); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetComment(ctx, c.ID)
		if err != nil || got.EditSeq != 2 || got.SyncState != step.want || got.RemoteURL != "https://gh/c/1" {
			t.Fatalf("ack seq %d %s = %+v %v, want %s", step.seq, step.state, got, err, step.want)
		}
	}
	if err := s.AckCommentSync(ctx, 9999, 0, SyncSynced, "", "", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AckCommentSync(missing) = %v, want ErrNotFound", err)
	}

	conv, _, err := s.UpsertRemoteComment(ctx, Comment{VersionID: v.ID, SectionID: sec.ID, Side: "additions", Subject: "file", Body: "x", RemoteID: "IC_1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCommentStatus(ctx, conv.ID, "resolved"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetComment(ctx, conv.ID); err != nil || got.EditSeq != 0 {
		t.Fatalf("local-only resolve = %+v %v, want edit seq untouched", got, err)
	}
}

func TestRemoteReplies(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	_, v, sec := seedPRVersion(ctx, t, s)
	commentID, err := s.CreateComment(ctx, Comment{VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "q"})
	if err != nil {
		t.Fatal(err)
	}
	in := Reply{
		CommentID: commentID, Origin: AuthorRemote, Kind: "note", Body: "agreed", RemoteID: "PRRC_10",
		RemoteURL: "https://gh/c/10", AuthorLogin: "octo", AuthorAvatarURL: "https://avatars/octo",
	}
	first, created, err := s.UpsertRemoteReply(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !created || first.Origin != AuthorRemote || first.SyncState != SyncSynced || first.RemoteID != "PRRC_10" || first.AuthorLogin != "octo" {
		t.Fatalf("first reply upsert = %+v created=%v", first, created)
	}
	in.Body = "agreed, thanks"
	second, created, err := s.UpsertRemoteReply(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID || second.Body != "agreed, thanks" {
		t.Fatalf("second reply upsert = %+v created=%v, want update of %d", second, created, first.ID)
	}
	if _, _, err := s.UpsertRemoteReply(ctx, Reply{CommentID: commentID, Origin: AuthorRemote, Kind: "note"}); err == nil {
		t.Fatal("UpsertRemoteReply accepted an empty remote id")
	}

	postingID, _, err := s.CreateReply(ctx, Reply{CommentID: commentID, Origin: AuthorClaude, Kind: "note", Body: "fixed", SyncState: SyncPosting})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetReplySync(ctx, postingID, SyncSynced, "PRRC_11", "https://gh/c/11", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReplySync(ctx, postingID, SyncFailed, "", "", "boom"); err != nil {
		t.Fatal(err)
	}
	posted, err := s.GetReply(ctx, postingID)
	if err != nil {
		t.Fatal(err)
	}
	if posted.SyncState != SyncFailed || posted.SyncError != "boom" || posted.RemoteID != "PRRC_11" || posted.RemoteURL != "https://gh/c/11" {
		t.Fatalf("posted reply = %+v", posted)
	}
	if err := s.SetReplySync(ctx, 9999, SyncSynced, "", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetReplySync(missing) = %v, want ErrNotFound", err)
	}
	replies, err := s.ListRepliesByComment(ctx, commentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 {
		t.Fatalf("replies = %d, want 2", len(replies))
	}
}

func TestUpsertPullRequest(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID := seedReview(ctx, t, s, "s", 0, "/repo", "feat", "base0")
	updated := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	pr := PullRequest{
		ReviewID: reviewID, Number: 12, NodeID: "PR_12", Title: "feat: a", Body: "body", State: "OPEN", URL: "https://gh/pull/12",
		AuthorLogin: "me", HeadRefName: "feat", HeadSHA: "abc", BaseRefName: "main", Mergeable: "MERGEABLE",
		Checks:         []PRCheck{{Name: "ci", State: "SUCCESS", URL: "https://ci/1"}},
		Reviewers:      []PRReviewer{{Login: "octo", AvatarURL: "https://avatars/octo", State: "APPROVED"}},
		ViewerIsAuthor: true, UpdatedAt: updated,
	}
	changed, err := s.UpsertPullRequest(ctx, pr)
	if err != nil || !changed {
		t.Fatalf("first UpsertPullRequest = %v %v, want changed", changed, err)
	}
	if changed, err = s.UpsertPullRequest(ctx, pr); err != nil || changed {
		t.Fatalf("repeat UpsertPullRequest = %v %v, want unchanged", changed, err)
	}
	pr.Draft, pr.Checks[0].State = true, "FAILURE"
	if changed, err = s.UpsertPullRequest(ctx, pr); err != nil || !changed {
		t.Fatalf("edited UpsertPullRequest = %v %v, want changed", changed, err)
	}
	trunk := PullRequest{ReviewID: reviewID, Number: 3, Title: "base", UpdatedAt: updated}
	if _, err := s.UpsertPullRequest(ctx, trunk); err != nil {
		t.Fatal(err)
	}
	got, err := s.PullRequests(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	trunk.Checks, trunk.Reviewers = []PRCheck{}, []PRReviewer{}
	want := []PullRequest{trunk, pr}
	for i := range want {
		want[i].UpdatedAt = fromUnix(unix(want[i].UpdatedAt))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PullRequests = %+v\nwant %+v", got, want)
	}
}
