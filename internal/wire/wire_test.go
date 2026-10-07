package wire

import (
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/store"
)

func TestSyncEventPayloads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{
			"comment",
			CommentSyncedFields(store.Comment{ID: 4, SyncState: store.SyncFailed, SyncError: "422", RemoteURL: ""}),
			`{"commentId":"4","remoteUrl":"","syncError":"422","syncState":"failed","type":"comment.synced","version_number":2}`,
		},
		{
			"reply",
			ReplySyncedFields(store.Reply{ID: 9, CommentID: 4, SyncState: store.SyncSynced, RemoteURL: "https://gh/c/9"}),
			`{"commentId":"4","remoteUrl":"https://gh/c/9","replyId":"9","syncError":"","syncState":"synced","type":"comment.synced","version_number":2}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(Event(store.EventCommentSynced, 2, tc.fields)); got != tc.want {
				t.Fatalf("frame = %s\nwant    %s", got, tc.want)
			}
		})
	}
}

func TestPRUpdatedPayloadHasConcreteArrays(t *testing.T) {
	pr := store.PullRequest{Number: 3, Title: "t", UpdatedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)}
	got := string(Event(store.EventPRUpdated, 1, PRUpdatedFields(pr)))
	want := `{"pullRequest":{"number":3,"title":"t","body":"","state":"","draft":false,"url":"","authorLogin":"","headRefName":"","headSha":"","baseRefName":"","mergeable":"","checks":[],"reviewers":[],"viewerIsAuthor":false,"updatedAt":"2026-10-06T00:00:00Z"},"type":"pr.updated","version_number":1}`
	if got != want {
		t.Fatalf("frame = %s\nwant    %s", got, want)
	}
}

func TestOriginFoldsAuthorsOntoSides(t *testing.T) {
	for author, want := range map[string]string{
		store.AuthorUser: "user", store.AuthorRemote: "user", store.AuthorClaude: "claude",
	} {
		c := ToComment(store.Comment{Author: author}, []store.Reply{{Origin: author}})
		if c.Origin != want || c.Author != author || c.Replies[0].Origin != want || c.Replies[0].Author != author {
			t.Fatalf("author %s: comment origin/author = %s/%s, reply = %s/%s", author, c.Origin, c.Author, c.Replies[0].Origin, c.Replies[0].Author)
		}
	}
}
