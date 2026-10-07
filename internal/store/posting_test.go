package store

import (
	"context"
	"reflect"
	"testing"
)

func TestPostingWritesListsInterruptedCommentsAndReplies(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID, v, sec := seedPRVersion(ctx, t, s)
	comment := func(state string) int64 {
		id, err := s.CreateComment(ctx, Comment{VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "b", SyncState: state})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	posting, synced := comment(SyncPosting), comment(SyncSynced)
	reply := func(commentID int64, state string) int64 {
		id, _, err := s.CreateReply(ctx, Reply{CommentID: commentID, Origin: AuthorUser, Kind: "note", Body: "r", SyncState: state})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	postingReply := reply(synced, SyncPosting)
	reply(synced, SyncFailed)

	got, err := s.PostingWrites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []PostingWrite{{ReviewID: reviewID, CommentID: posting}, {ReviewID: reviewID, CommentID: synced, ReplyID: postingReply}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PostingWrites = %+v, want %+v", got, want)
	}
}

func TestRemoteIDClaimed(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	reviewID, v, sec := seedPRVersion(ctx, t, s)
	id, err := s.CreateComment(ctx, Comment{VersionID: v.ID, SectionID: sec.ID, FilePath: "a.go", Side: "additions", StartLine: 1, EndLine: 1, Body: "b", SyncState: SyncPosting})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommentSync(ctx, id, SyncSynced, "PRRC_1", "PRRT_1", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertRemoteReply(ctx, Reply{CommentID: id, Origin: AuthorRemote, Kind: "note", Body: "r", RemoteID: "PRRC_2"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		reviewID, remoteID string
		want               bool
	}{
		{reviewID, "PRRC_1", true},
		{reviewID, "PRRC_2", true},
		{reviewID, "PRRC_3", false},
		{"other-review", "PRRC_1", false},
	} {
		if got, err := s.RemoteIDClaimed(ctx, tc.reviewID, tc.remoteID); err != nil || got != tc.want {
			t.Errorf("RemoteIDClaimed(%s, %s) = %v, %v; want %v", tc.reviewID, tc.remoteID, got, err, tc.want)
		}
	}
}
