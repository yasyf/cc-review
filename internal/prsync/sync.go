package prsync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

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

	prOpen = "OPEN"
)

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
	repo, err := github.ParseRepo(meta.Repo)
	if err != nil {
		return false, err
	}
	g := s.gate(p.reviewID)
	recaptured := false
	for {
		writes := g.writes.Load()
		v, sections, snaps, err := s.fetch(ctx, st, p.reviewID, repo)
		if err != nil {
			return true, err
		}
		moved, err := s.stackMoved(ctx, repo, meta.PRNumber, sections, snaps)
		if err != nil {
			return true, err
		}
		if moved {
			if recaptured {
				return true, errors.New("the pull request stack moved again during recapture")
			}
			if err := s.cfg.Recapture(ctx, p.reviewID); err != nil {
				return true, fmt.Errorf("recapture: %w", err)
			}
			recaptured = true
			continue
		}
		g.mu.Lock()
		if g.writes.Load() != writes {
			g.mu.Unlock()
			continue
		}
		err = s.apply(ctx, st, p, v, sections, snaps)
		g.mu.Unlock()
		if err != nil {
			return true, err
		}
		return true, s.publish(ctx, st, p.reviewID)
	}
}

func (s *Syncer) fetch(ctx context.Context, st *store.Store, reviewID string, repo github.Repo) (store.Version, []store.Section, map[int]github.PRSnapshot, error) {
	v, sections, err := latestSections(ctx, st, reviewID)
	if err != nil {
		return store.Version{}, nil, nil, err
	}
	snaps, err := s.cfg.Client.Snapshot(ctx, repo, prNumbers(sections))
	if err != nil {
		return store.Version{}, nil, nil, fmt.Errorf("snapshot %s: %w", repo, err)
	}
	return v, sections, snaps, nil
}

func (s *Syncer) stackMoved(ctx context.Context, repo github.Repo, target int, sections []store.Section, snaps map[int]github.PRSnapshot) (bool, error) {
	for _, sec := range sections {
		pr := snaps[sec.PRNumber].PR
		if pr.HeadRefOid != sec.HeadRef || pr.BaseRefName != sec.ParentBranch || (pr.Number != target && pr.State != prOpen) {
			return true, nil
		}
	}
	top := sections[len(sections)-1].Branch
	children, err := s.cfg.Client.OpenPRsWithBase(ctx, repo, top)
	if err != nil {
		return false, fmt.Errorf("pull requests on %s: %w", top, err)
	}
	return len(children) == 1 && !slices.ContainsFunc(sections, func(sec store.Section) bool {
		return sec.PRNumber == children[0].Number
	}), nil
}

func (s *Syncer) apply(ctx context.Context, st *store.Store, p *poller, v store.Version, sections []store.Section, snaps map[int]github.PRSnapshot) error {
	return st.ApplyRemote(ctx, func(rt *store.RemoteTx) error {
		sc := syncCtx{rt: rt, p: p, version: v}
		if err := sc.pullRequests(ctx, snaps); err != nil {
			return err
		}
		for _, sec := range sections {
			snap := snaps[sec.PRNumber]
			for _, th := range snap.Threads {
				if err := sc.thread(ctx, sec, th); err != nil {
					return err
				}
			}
			for _, ic := range snap.IssueComments {
				if err := sc.issueComment(ctx, sec, ic); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Syncer) publish(ctx context.Context, st *store.Store, reviewID string) error {
	pending, err := st.PendingEvents(ctx, reviewID)
	if err != nil {
		return err
	}
	for _, pe := range pending {
		seq, err := s.cfg.Append(ctx, &ccevent.Event{
			SubjectID: pe.ReviewID, Origin: pe.Origin, Type: pe.Type, Payload: pe.Payload, DedupKey: pe.DedupKey(),
		})
		if err != nil {
			return fmt.Errorf("append %s: %w", pe.Type, err)
		}
		if err := st.MarkEventAppended(ctx, pe.ID, seq); err != nil {
			return err
		}
	}
	return nil
}

type syncCtx struct {
	rt      *store.RemoteTx
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

func prNumbers(sections []store.Section) []int {
	out := make([]int, len(sections))
	for i, sec := range sections {
		out[i] = sec.PRNumber
	}
	return out
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
		RemoteID: root.NodeID, RemoteThreadID: th.NodeID, RemoteURL: root.URL,
		AuthorLogin: root.AuthorLogin, AuthorAvatarURL: root.AuthorAvatarURL,
		Outdated: outdated, Subject: subject, SyncState: store.SyncSynced,
	}
	prev, existed, err := sc.existing(ctx, c)
	if err != nil {
		return err
	}
	prevReplies := map[string]store.Reply{}
	if existed {
		rs, err := sc.rt.ListRepliesByComment(ctx, prev.ID)
		if err != nil {
			return err
		}
		for _, r := range rs {
			prevReplies[r.RemoteID] = r
		}
	}
	saved, created, err := sc.rt.UpsertComment(ctx, c)
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
			RemoteID: rc.NodeID, RemoteURL: rc.URL,
			AuthorLogin: rc.AuthorLogin, AuthorAvatarURL: rc.AuthorAvatarURL, SyncState: store.SyncSynced,
		}
		before, had := prevReplies[r.RemoteID]
		if !had && rAuthor != store.AuthorRemote {
			if before, had, err = sc.adoptReply(ctx, sec, false, r); err != nil {
				return err
			}
		}
		after, _, err := sc.rt.UpsertReply(ctx, r)
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
		return sc.emitComment(ctx, ch.origin(), store.EventCommentCreated, saved)
	case statusOnly && saved.Status == "resolved":
		return sc.emit(ctx, ccevent.OriginHuman, store.EventCommentResolved, map[string]any{"commentId": strconv.FormatInt(saved.ID, 10)})
	case ch.any():
		return sc.emitComment(ctx, ch.origin(), store.EventCommentUpdated, saved)
	}
	return nil
}

func (sc syncCtx) issueComment(ctx context.Context, sec store.Section, ic github.RemoteComment) error {
	if _, err := sc.rt.ReplyByRemoteID(ctx, sc.p.reviewID, ic.NodeID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	author, origin := sc.p.authorOf(ic.AuthorLogin)
	c := store.Comment{
		VersionID: sc.version.ID, SectionID: sec.ID, Branch: sec.Key(), Pending: sec.Pending,
		Side: "additions", Body: ic.Body, Author: author, Status: "open",
		RemoteID: ic.NodeID, RemoteURL: ic.URL,
		AuthorLogin: ic.AuthorLogin, AuthorAvatarURL: ic.AuthorAvatarURL,
		Subject: subjectFile, SyncState: store.SyncSynced,
	}
	prev, existed, err := sc.existing(ctx, c)
	if err != nil {
		return err
	}
	if !existed && author != store.AuthorRemote {
		reply := store.Reply{Origin: author, Body: ic.Body, RemoteID: ic.NodeID, RemoteURL: ic.URL}
		if _, adopted, err := sc.adoptReply(ctx, sec, true, reply); err != nil || adopted {
			return err
		}
	}
	if existed {
		c.Status = prev.Status
	}
	saved, created, err := sc.rt.UpsertComment(ctx, c)
	if err != nil {
		return fmt.Errorf("upsert issue comment %s: %w", ic.NodeID, err)
	}
	switch {
	case created:
		return sc.emitComment(ctx, origin, store.EventCommentCreated, saved)
	case existed && diffComment(prev, saved).content:
		return sc.emitComment(ctx, origin, store.EventCommentUpdated, saved)
	}
	return nil
}

func (sc syncCtx) existing(ctx context.Context, c store.Comment) (store.Comment, bool, error) {
	prev, err := sc.rt.CommentByRemoteID(ctx, sc.p.reviewID, c.RemoteID)
	switch {
	case err == nil:
		return prev, true, nil
	case !errors.Is(err, store.ErrNotFound):
		return store.Comment{}, false, err
	case c.Author == store.AuthorRemote:
		return store.Comment{}, false, nil
	}
	adopted, ok, err := sc.rt.AdoptComment(ctx, sc.p.reviewID, c)
	if err != nil || !ok {
		return store.Comment{}, false, err
	}
	return adopted, true, sc.emit(ctx, ccevent.OriginAgent, store.EventCommentSynced, wire.CommentSyncedFields(adopted))
}

func (sc syncCtx) adoptReply(ctx context.Context, sec store.Section, conversation bool, r store.Reply) (store.Reply, bool, error) {
	adopted, ok, err := sc.rt.AdoptReply(ctx, sc.p.reviewID, sec.Key(), conversation, r)
	if err != nil || !ok {
		return store.Reply{}, false, err
	}
	return adopted, true, sc.emit(ctx, ccevent.OriginAgent, store.EventCommentSynced, wire.ReplySyncedFields(adopted))
}

func (sc syncCtx) pullRequests(ctx context.Context, snaps map[int]github.PRSnapshot) error {
	for n, snap := range snaps {
		pr := PullRequestRow(sc.p.reviewID, snap.PR, sc.p.viewer)
		changed, err := sc.rt.UpsertPullRequest(ctx, pr)
		if err != nil {
			return fmt.Errorf("upsert pull request #%d: %w", n, err)
		}
		if changed {
			if err := sc.emit(ctx, ccevent.OriginSystem, store.EventPRUpdated, wire.PRUpdatedFields(pr)); err != nil {
				return err
			}
		}
	}
	return nil
}

// PullRequestRow is the stored form of a PR fetched from GitHub; viewer is the
// GitHub login the daemon reads as, which decides ViewerIsAuthor.
func PullRequestRow(reviewID string, pr github.PullRequest, viewer string) store.PullRequest {
	checks := make([]store.PRCheck, len(pr.Checks))
	for i, c := range pr.Checks {
		checks[i] = store.PRCheck{Name: c.Name, State: c.State, URL: c.URL}
	}
	reviewers := make([]store.PRReviewer, len(pr.Reviewers))
	for i, r := range pr.Reviewers {
		reviewers[i] = store.PRReviewer{Login: r.Login, AvatarURL: r.AvatarURL, State: r.State}
	}
	return store.PullRequest{
		ReviewID: reviewID, Number: pr.Number, NodeID: pr.NodeID, Title: pr.Title, Body: pr.Body, State: pr.State,
		URL: pr.URL, AuthorLogin: pr.AuthorLogin, HeadRefName: pr.HeadRefName, HeadSHA: pr.HeadRefOid,
		BaseRefName: pr.BaseRefName, Draft: pr.Draft, Mergeable: pr.Mergeable, Checks: checks, Reviewers: reviewers,
		ViewerIsAuthor: pr.AuthorLogin == viewer, UpdatedAt: pr.UpdatedAt,
	}
}

func (sc syncCtx) emitComment(ctx context.Context, origin, typ string, c store.Comment) error {
	replies, err := sc.rt.ListRepliesByComment(ctx, c.ID)
	if err != nil {
		return err
	}
	return sc.emit(ctx, origin, typ, map[string]any{
		"commentId": strconv.FormatInt(c.ID, 10), "comment": wire.ToComment(c, replies),
	})
}

func (sc syncCtx) emit(ctx context.Context, origin, typ string, fields map[string]any) error {
	return sc.rt.QueueEvent(ctx, sc.p.reviewID, origin, typ, wire.Event(typ, sc.version.VersionNumber, fields))
}
