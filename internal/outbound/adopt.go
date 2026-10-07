package outbound

import (
	"context"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/store"
)

func (s *Syncer) findComment(ctx context.Context, st *store.Store, c store.Comment) (github.RemoteComment, string, bool, error) {
	pr, _, _, err := s.target(ctx, st, c, c.Author)
	if err != nil {
		return github.RemoteComment{}, "", false, err
	}
	login, snap, err := s.remoteState(ctx, pr, c.Author)
	if err != nil {
		return github.RemoteComment{}, "", false, err
	}
	reviewID, _, err := st.ResolveCommentContext(ctx, c.ID)
	if err != nil {
		return github.RemoteComment{}, "", false, err
	}
	if conversation(c) {
		remote, ok, err := unclaimed(ctx, st, reviewID, snap.IssueComments, login, c.Body)
		return remote, "", ok, err
	}
	anchor := newReviewComment(c, "")
	for _, th := range snap.Threads {
		if !sameAnchor(th, anchor) {
			continue
		}
		remote, ok, err := unclaimed(ctx, st, reviewID, th.Comments[:1], login, c.Body)
		if err != nil || ok {
			return remote, th.NodeID, ok, err
		}
	}
	return github.RemoteComment{}, "", false, nil
}

func (s *Syncer) findReply(ctx context.Context, st *store.Store, r store.Reply) (github.RemoteComment, bool, error) {
	parent, err := st.GetComment(ctx, r.CommentID)
	if err != nil {
		return github.RemoteComment{}, false, err
	}
	pr, _, _, err := s.target(ctx, st, parent, r.Origin)
	if err != nil {
		return github.RemoteComment{}, false, err
	}
	login, snap, err := s.remoteState(ctx, pr, r.Origin)
	if err != nil {
		return github.RemoteComment{}, false, err
	}
	reviewID, _, err := st.ResolveCommentContext(ctx, parent.ID)
	if err != nil {
		return github.RemoteComment{}, false, err
	}
	if conversation(parent) {
		return unclaimed(ctx, st, reviewID, snap.IssueComments, login, r.GitHubBody())
	}
	for _, th := range snap.Threads {
		if th.NodeID == parent.RemoteThreadID {
			return unclaimed(ctx, st, reviewID, th.Comments[1:], login, r.GitHubBody())
		}
	}
	return github.RemoteComment{}, false, nil
}

func (s *Syncer) remoteState(ctx context.Context, pr github.PRRef, author string) (string, github.PRSnapshot, error) {
	login, err := s.login(ctx, pr.Repo, author)
	if err != nil {
		return "", github.PRSnapshot{}, err
	}
	snaps, err := s.user.Snapshot(ctx, pr.Repo, []int{pr.Number})
	if err != nil {
		return "", github.PRSnapshot{}, err
	}
	return login, snaps[pr.Number], nil
}

func (s *Syncer) login(ctx context.Context, repo github.Repo, author string) (string, error) {
	if author != store.AuthorClaude {
		return s.user.Viewer(ctx)
	}
	_, login, err := s.app(ctx, repo)
	return login, err
}

func sameAnchor(th github.Thread, nc github.NewReviewComment) bool {
	if th.Path != nc.Path || (th.SubjectType == "FILE") != (nc.SubjectType == "FILE") {
		return false
	}
	if nc.SubjectType == "FILE" {
		return true
	}
	return th.OriginalLine == nc.Line && th.DiffSide == nc.Side &&
		th.OriginalStartLine == nc.StartLine && (nc.StartLine == 0 || th.StartDiffSide == nc.StartSide)
}

func unclaimed(ctx context.Context, st *store.Store, reviewID string, comments []github.RemoteComment, login, body string) (github.RemoteComment, bool, error) {
	for _, c := range comments {
		if c.AuthorLogin != login || c.Body != body {
			continue
		}
		claimed, err := st.RemoteIDClaimed(ctx, reviewID, c.NodeID)
		if err != nil {
			return github.RemoteComment{}, false, err
		}
		if !claimed {
			return c, true, nil
		}
	}
	return github.RemoteComment{}, false, nil
}
