package prsync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	ccevent "github.com/yasyf/cc-interact/event"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

const (
	statusOpen      = "open"
	statusSubmitted = "submitted"

	subjectLine = "line"
	subjectFile = "file"

	replyKindNote = "note"
)

// change accumulates what one thread's sync altered and under which origins.
type change struct{ touched, human bool }

func (c *change) add(origin string) {
	c.touched = true
	if origin != ccevent.OriginAgent {
		c.human = true
	}
}

func (c change) any() bool { return c.touched }

// origin is agent only when every alteration was the app bot's own or a
// re-anchor, so Claude never wakes on its own activity.
func (c change) origin() string {
	if c.human {
		return ccevent.OriginHuman
	}
	return ccevent.OriginAgent
}

type commentDiff struct{ content, status, anchor bool }

func diffComment(prev, next store.Comment) commentDiff {
	return commentDiff{
		content: prev.Body != next.Body || prev.AuthorAvatarURL != next.AuthorAvatarURL || prev.RemoteURL != next.RemoteURL,
		status:  prev.Status != next.Status,
		anchor: prev.VersionID != next.VersionID || prev.SectionID != next.SectionID || prev.FilePath != next.FilePath ||
			prev.Side != next.Side || prev.StartLine != next.StartLine || prev.EndLine != next.EndLine ||
			prev.StartSide != next.StartSide || prev.EndSide != next.EndSide ||
			prev.Outdated != next.Outdated || prev.Subject != next.Subject,
	}
}

func replyChanged(prev, next store.Reply) bool {
	return prev.Body != next.Body || prev.AuthorAvatarURL != next.AuthorAvatarURL || prev.RemoteURL != next.RemoteURL
}

// poll syncs one snapshot of the review's stack and reports whether the poller
// should keep running.
func (s *Syncer) poll(ctx context.Context, p *poller) (bool, error) {
	st := store.New(s.cfg.DB())
	rev, err := st.GetReview(ctx, p.reviewID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if rev.Status != statusOpen && rev.Status != statusSubmitted {
		return false, nil
	}
	meta, ok, err := st.GetReviewMeta(ctx, p.reviewID)
	if err != nil {
		return true, err
	}
	if !ok || meta.Kind != store.ReviewKindPR {
		return false, fmt.Errorf("review is not a pull-request review")
	}
	repo, err := parseRepo(meta.Repo)
	if err != nil {
		return false, err
	}
	v, sections, err := latestSections(ctx, st, p.reviewID)
	if err != nil {
		return true, err
	}
	snaps, err := s.cfg.Client.Snapshot(ctx, repo, prNumbers(sections))
	if err != nil {
		return true, fmt.Errorf("snapshot %s: %w", repo, err)
	}
	if headsMoved(sections, snaps) {
		if err := s.cfg.Recapture(ctx, p.reviewID); err != nil {
			return true, fmt.Errorf("recapture: %w", err)
		}
		if v, sections, err = latestSections(ctx, st, p.reviewID); err != nil {
			return true, err
		}
	}
	release := s.Exclusive(p.reviewID)
	defer release()
	sc := syncCtx{s: s, st: st, p: p, version: v}
	if err := sc.pullRequests(ctx, snaps); err != nil {
		return true, err
	}
	for _, sec := range sections {
		snap := snaps[sec.PRNumber]
		for _, th := range snap.Threads {
			if err := sc.thread(ctx, sec, th); err != nil {
				return true, err
			}
		}
		for _, ic := range snap.IssueComments {
			if err := sc.issueComment(ctx, sec, ic); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

type syncCtx struct {
	s       *Syncer
	st      *store.Store
	p       *poller
	version store.Version
}

func latestSections(ctx context.Context, st *store.Store, reviewID string) (store.Version, []store.Section, error) {
	v, ok, err := st.LatestVersion(ctx, reviewID)
	if err != nil {
		return store.Version{}, nil, err
	}
	if !ok {
		return store.Version{}, nil, fmt.Errorf("review %s has no versions", reviewID)
	}
	sections, err := st.ListSections(ctx, v.ID)
	if err != nil {
		return store.Version{}, nil, err
	}
	return v, sections, nil
}

func parseRepo(s string) (github.Repo, error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" {
		return github.Repo{}, fmt.Errorf("review repo %q is not owner/name", s)
	}
	return github.Repo{Owner: owner, Name: name}, nil
}

func prNumbers(sections []store.Section) []int {
	out := make([]int, len(sections))
	for i, sec := range sections {
		out[i] = sec.PRNumber
	}
	return out
}

func headsMoved(sections []store.Section, snaps map[int]github.PRSnapshot) bool {
	for _, sec := range sections {
		if snaps[sec.PRNumber].PR.HeadRefOid != sec.HeadRef {
			return true
		}
	}
	return false
}

// authorOf maps a GitHub login to the local author and the event origin its
// activity carries: the app's bot is Claude under the agent origin, so the
// channel's ExcludeOrigin=agent never echoes Claude's own comments back.
func (p *poller) authorOf(login string) (author, origin string) {
	switch login {
	case p.app.BotLogin:
		return store.AuthorClaude, ccevent.OriginAgent
	case p.viewer:
		return store.AuthorUser, ccevent.OriginHuman
	default:
		return store.AuthorRemote, ccevent.OriginHuman
	}
}

func sideOf(diffSide string) string {
	if diffSide == "LEFT" {
		return "deletions"
	}
	return "additions"
}

// reviewCommentID keys a review comment or reply by its database id, the id
// GitHub's reply endpoint takes.
func reviewCommentID(c github.RemoteComment) string {
	return strconv.FormatInt(c.DatabaseID, 10)
}

// issueCommentID keys an issue comment by its node id, which can never collide
// with a review comment's decimal database id.
func issueCommentID(c github.RemoteComment) string {
	return c.NodeID
}

// threadAnchor places a thread on the diff: a live line thread on its lines,
// an outdated or file-subject thread on the file header keeping its original
// lines for display.
func threadAnchor(th github.Thread) (subject string, outdated bool, start, end int, startSide, endSide string) {
	side := sideOf(th.DiffSide)
	startSide = side
	if th.StartDiffSide != "" {
		startSide = sideOf(th.StartDiffSide)
	}
	switch {
	case th.IsOutdated:
		end = th.OriginalLine
		start = th.OriginalStartLine
		subject = subjectFile
	case th.SubjectType == "FILE":
		subject = subjectFile
	default:
		end = th.Line
		start = th.StartLine
		subject = subjectLine
	}
	if start == 0 {
		start = end
	}
	return subject, th.IsOutdated, start, end, startSide, side
}

func (sc syncCtx) thread(ctx context.Context, sec store.Section, th github.Thread) error {
	if len(th.Comments) == 0 {
		return nil
	}
	root := th.Comments[0]
	author, rootOrigin := sc.p.authorOf(root.AuthorLogin)
	subject, outdated, start, end, startSide, endSide := threadAnchor(th)
	status := "open"
	if th.IsResolved {
		status = "resolved"
	}
	c := store.Comment{
		VersionID: sc.version.ID, SectionID: sec.ID, Branch: sec.Key(), Pending: sec.Pending,
		FilePath: th.Path, Side: endSide, StartLine: start, EndLine: end, StartSide: startSide, EndSide: endSide,
		Body: root.Body, Author: author, Status: status,
		RemoteID: reviewCommentID(root), RemoteThreadID: th.NodeID, RemoteURL: root.URL,
		AuthorLogin: root.AuthorLogin, AuthorAvatarURL: root.AuthorAvatarURL,
		Outdated: outdated, Subject: subject, SyncState: store.SyncSynced,
	}
	prev, err := sc.st.CommentByRemoteID(ctx, sc.p.reviewID, c.RemoteID)
	existed := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	prevReplies := map[string]store.Reply{}
	if existed {
		rs, err := sc.st.ListRepliesByComment(ctx, prev.ID)
		if err != nil {
			return err
		}
		for _, r := range rs {
			prevReplies[r.RemoteID] = r
		}
	}
	saved, created, err := sc.st.UpsertRemoteComment(ctx, c)
	if err != nil {
		return fmt.Errorf("upsert thread %s: %w", th.NodeID, err)
	}
	var (
		ch change
		d  commentDiff
	)
	if created {
		ch.add(rootOrigin)
	} else {
		d = diffComment(prev, saved)
		if d.content {
			ch.add(rootOrigin)
		}
		if d.status {
			ch.add(ccevent.OriginHuman)
		}
		if d.anchor {
			ch.add(ccevent.OriginAgent)
		}
	}
	statusOnly := d.status && !d.content && !d.anchor
	for _, rc := range th.Comments[1:] {
		rAuthor, rOrigin := sc.p.authorOf(rc.AuthorLogin)
		r := store.Reply{
			CommentID: saved.ID, Origin: rAuthor, Kind: replyKindNote, Body: rc.Body,
			RemoteID: reviewCommentID(rc), RemoteURL: rc.URL,
			AuthorLogin: rc.AuthorLogin, AuthorAvatarURL: rc.AuthorAvatarURL, SyncState: store.SyncSynced,
		}
		before, had := prevReplies[r.RemoteID]
		after, _, err := sc.st.UpsertRemoteReply(ctx, r)
		if err != nil {
			return fmt.Errorf("upsert reply %s: %w", r.RemoteID, err)
		}
		if !had || replyChanged(before, after) {
			ch.add(rOrigin)
			statusOnly = false
		}
	}
	switch {
	case created:
		return sc.emitComment(ctx, ch.origin(), store.EventCommentCreated, saved.ID)
	case statusOnly && saved.Status == "resolved":
		sc.emit(ctx, ccevent.OriginHuman, store.EventCommentResolved, map[string]any{"commentId": strconv.FormatInt(saved.ID, 10)})
		return nil
	case ch.any():
		return sc.emitComment(ctx, ch.origin(), store.EventCommentUpdated, saved.ID)
	}
	return nil
}

func (sc syncCtx) issueComment(ctx context.Context, sec store.Section, ic github.RemoteComment) error {
	author, origin := sc.p.authorOf(ic.AuthorLogin)
	c := store.Comment{
		VersionID: sc.version.ID, SectionID: sec.ID, Branch: sec.Key(), Pending: sec.Pending,
		Side: "additions", Body: ic.Body, Author: author, Status: "open",
		RemoteID: issueCommentID(ic), RemoteURL: ic.URL,
		AuthorLogin: ic.AuthorLogin, AuthorAvatarURL: ic.AuthorAvatarURL,
		Subject: subjectFile, SyncState: store.SyncSynced,
	}
	prev, err := sc.st.CommentByRemoteID(ctx, sc.p.reviewID, c.RemoteID)
	existed := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	saved, created, err := sc.st.UpsertRemoteComment(ctx, c)
	if err != nil {
		return fmt.Errorf("upsert issue comment %s: %w", ic.NodeID, err)
	}
	switch {
	case created:
		return sc.emitComment(ctx, origin, store.EventCommentCreated, saved.ID)
	case existed && diffComment(prev, saved).content:
		return sc.emitComment(ctx, origin, store.EventCommentUpdated, saved.ID)
	}
	return nil
}

func (sc syncCtx) pullRequests(ctx context.Context, snaps map[int]github.PRSnapshot) error {
	for n, snap := range snaps {
		pr := sc.toStorePR(snap.PR)
		changed, err := sc.st.UpsertPullRequest(ctx, pr)
		if err != nil {
			return fmt.Errorf("upsert pull request #%d: %w", n, err)
		}
		if changed {
			sc.emit(ctx, ccevent.OriginSystem, store.EventPRUpdated, wire.PRUpdatedFields(pr))
		}
	}
	return nil
}

func (sc syncCtx) toStorePR(pr github.PullRequest) store.PullRequest {
	checks := make([]store.PRCheck, len(pr.Checks))
	for i, c := range pr.Checks {
		checks[i] = store.PRCheck{Name: c.Name, State: c.State, URL: c.URL}
	}
	reviewers := make([]store.PRReviewer, len(pr.Reviewers))
	for i, r := range pr.Reviewers {
		reviewers[i] = store.PRReviewer{Login: r.Login, AvatarURL: r.AvatarURL, State: r.State}
	}
	return store.PullRequest{
		ReviewID: sc.p.reviewID, Number: pr.Number, NodeID: pr.NodeID, Title: pr.Title, Body: pr.Body, State: pr.State,
		URL: pr.URL, AuthorLogin: pr.AuthorLogin, HeadRefName: pr.HeadRefName, HeadSHA: pr.HeadRefOid,
		BaseRefName: pr.BaseRefName, Draft: pr.Draft, Mergeable: pr.Mergeable, Checks: checks, Reviewers: reviewers,
		ViewerIsAuthor: pr.AuthorLogin == sc.p.viewer, UpdatedAt: pr.UpdatedAt,
	}
}

func (sc syncCtx) emitComment(ctx context.Context, origin, typ string, commentID int64) error {
	c, err := sc.st.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	replies, err := sc.st.ListRepliesByComment(ctx, commentID)
	if err != nil {
		return err
	}
	sc.emit(ctx, origin, typ, map[string]any{
		"commentId": strconv.FormatInt(commentID, 10), "comment": wire.ToComment(c, replies),
	})
	return nil
}

func (sc syncCtx) emit(ctx context.Context, origin, typ string, fields map[string]any) {
	_, _ = sc.s.cfg.Append(ctx, &ccevent.Event{
		SubjectID: sc.p.reviewID, Origin: origin, Type: typ, Payload: wire.Event(typ, sc.version.VersionNumber, fields),
	})
}
