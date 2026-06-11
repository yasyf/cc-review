package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yasyf/cc-review/internal/feedback"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/session"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/vcs"
	"github.com/yasyf/cc-review/internal/version"
	"github.com/yasyf/cc-review/internal/wire"
)

func win(req Request) session.Window {
	return session.Window{SessionID: req.Session, ClaudePID: req.ClaudePID}
}

func (s *Server) handleStart(ctx context.Context, req Request) Response {
	if req.Cwd == "" {
		return errResp("start requires --cwd")
	}
	root, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return errResp(err.Error())
	}
	// Capture before any resolver write: a failed (e.g. empty) snapshot must
	// create nothing — and must not let --new close the prior review. A resumed
	// review captures against its pinned base, so the peek comes first.
	var snap vcs.Snapshot
	fromPin := false
	if peeked, ok, err := s.resolver.Peek(ctx, win(req), root); err != nil {
		return errResp(err.Error())
	} else if ok && !req.New {
		if peeked.BaseRef == "" {
			return errResp(fmt.Sprintf("review %s predates pinned diff bases; pass --new to start a fresh review", peeked.Slug))
		}
		if req.Base != "" {
			return errResp(fmt.Sprintf("review %s is pinned to base %s; pass --new to start a fresh review with --base", peeked.Slug, peeked.BaseRef))
		}
		fromPin = true
		if snap, err = vcs.CaptureAt(ctx, req.Cwd, peeked.BaseRef); err != nil {
			if !errors.Is(err, vcs.ErrNoChanges) {
				return errResp(err.Error() + " (pass --new to start a fresh review)")
			}
			return errResp(err.Error())
		}
	} else {
		if snap, err = vcs.Capture(ctx, req.Cwd, req.Base); err != nil {
			return errResp(err.Error())
		}
	}
	review, resumed, err := s.resolver.Start(ctx, win(req), snap.RepoRoot, snap.Branch, snap.BaseRef, req.New)
	if err != nil {
		return errResp(err.Error())
	}
	// The peek's verdict can flip under a concurrent rebind, adopt, or submit
	// between the read and the write phase; re-align the snapshot with the
	// review Start actually returned.
	if resumed {
		// Peek said create (so --base passed the gate above) but Start resumed
		// an existing pinned review: the explicit base cannot apply.
		if req.Base != "" {
			return errResp(fmt.Sprintf("review %s is pinned to base %s; pass --new to start a fresh review with --base", review.Slug, review.BaseRef))
		}
		if review.BaseRef != snap.BaseRef {
			if snap, err = vcs.CaptureAt(ctx, req.Cwd, review.BaseRef); err != nil {
				return errResp(err.Error())
			}
		}
	} else if fromPin {
		// Peek said resume but Start created: the snapshot was taken against
		// the vanished review's pin; recapture with create semantics and re-pin
		// the just-created (still version-less) review.
		if snap, err = vcs.Capture(ctx, req.Cwd, ""); err != nil {
			// Leave nothing adoptable behind: the empty review would otherwise
			// be resumed against its stale foreign pin on the next start.
			if cerr := s.store.SetReviewStatus(ctx, review.ID, "closed"); cerr != nil {
				return errResp(cerr.Error())
			}
			if derr := s.store.DetachReviewSession(ctx, review.ID); derr != nil {
				return errResp(derr.Error())
			}
			return errResp(err.Error())
		}
		if err := s.store.SetReviewBaseRef(ctx, review.ID, snap.BaseRef); err != nil {
			return errResp(err.Error())
		}
		review.BaseRef = snap.BaseRef
	}
	// An unchanged worktree on resume reuses the latest version instead of
	// stacking an identical one and re-queueing an organize request. A version
	// whose patch file is unreadable (crash between insert and rename) just
	// misses the dedup and gets a fresh version.
	if resumed {
		if latest, ok, err := s.store.LatestVersion(ctx, review.ID); err != nil {
			return errResp(err.Error())
		} else if ok {
			if prev, err := os.ReadFile(latest.PatchPath); err == nil && string(prev) == snap.PatchText {
				// A successful start always leaves the round open: resuming a
				// submitted review must re-block edits even when the snapshot
				// is unchanged.
				if review.Status != "open" {
					if err := s.store.SetReviewStatus(ctx, review.ID, "open"); err != nil {
						return errResp(err.Error())
					}
				}
				return Response{
					OK: true, URL: s.reviewURL(review.Slug), ReviewID: review.ID, Version: latest.VersionNumber, Resumed: true,
					HTTPPort:      s.httpPort,
					ChannelActive: s.channelActive(ctx, review.ID, snap.RepoRoot, req.ClaudePID),
				}
			}
		}
	}
	if err := paths.EnsureReviewDir(review.ID); err != nil {
		return errResp(err.Error())
	}
	filesJSON, err := json.Marshal(snap.Files)
	if err != nil {
		return errResp(err.Error())
	}
	// Write the patch to a temp file before inserting the version row, so a write
	// failure can never leave behind a committed-but-unreadable version. The row
	// then gets the final path after an atomic rename into place.
	tmp, err := os.CreateTemp(paths.ReviewDir(review.ID), "snap-*.tmp")
	if err != nil {
		return errResp(err.Error())
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(snap.PatchText); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return errResp(err.Error())
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return errResp(err.Error())
	}
	v, err := s.store.CreateVersion(ctx, review.ID, snap.Branch, snap.BaseRef, "", string(filesJSON))
	if err != nil {
		os.Remove(tmpName)
		return errResp(err.Error())
	}
	patchPath := paths.SnapshotPath(review.ID, v.VersionNumber)
	if err := os.Rename(tmpName, patchPath); err != nil {
		os.Remove(tmpName)
		return errResp(err.Error())
	}
	if err := s.store.UpdateVersionPatchPath(ctx, v.ID, patchPath); err != nil {
		return errResp(err.Error())
	}
	// A new version reopens the review (a prior round may have been submitted), so
	// the edit guard blocks edits again until this round is submitted.
	if review.Status != "open" {
		if err := s.store.SetReviewStatus(ctx, review.ID, "open"); err != nil {
			return errResp(err.Error())
		}
	}
	// Carry review state across versions: unmark files whose diff content
	// changed (version.created first, then the unmark batch), then queue the
	// system organize request for the live Claude session.
	fingerprints := make(map[string]string, len(snap.Files))
	for _, f := range snap.Files {
		fingerprints[f.Path] = f.Fingerprint
	}
	unmarked, err := s.store.UnreviewChangedFiles(ctx, review.ID, fingerprints)
	if err != nil {
		return errResp(err.Error())
	}
	if _, err := s.AppendEvent(ctx, &store.Event{
		ReviewID: review.ID, Origin: store.OriginSystem, Type: store.EventVersionCreated, VersionNumber: v.VersionNumber,
		Payload: wire.Event(store.EventVersionCreated, v.VersionNumber, nil),
	}); err != nil {
		return errResp(err.Error())
	}
	if len(unmarked) > 0 {
		states := make([]map[string]any, 0, len(unmarked))
		for _, st := range unmarked {
			states = append(states, map[string]any{"path": st.Path, "reviewed": false, "hidden": st.Hidden})
		}
		if _, err := s.AppendEvent(ctx, &store.Event{
			ReviewID: review.ID, Origin: store.OriginSystem, Type: store.EventFileStates, VersionNumber: v.VersionNumber,
			Payload: wire.Event(store.EventFileStates, v.VersionNumber, map[string]any{"states": states}),
		}); err != nil {
			return errResp(err.Error())
		}
	}
	organize, err := s.store.CreateAIRequest(ctx, review.ID, v.VersionNumber, store.OriginSystem, organizePrompt)
	if err != nil {
		return errResp(err.Error())
	}
	s.emitAIRequest(ctx, store.OriginSystem, store.EventAIRequestCreated, v.VersionNumber, organize)
	return Response{
		OK: true, URL: s.reviewURL(review.Slug), ReviewID: review.ID, Version: v.VersionNumber, Resumed: resumed,
		HTTPPort:      s.httpPort,
		ChannelActive: s.channelActive(ctx, review.ID, snap.RepoRoot, req.ClaudePID),
	}
}

func (s *Server) reviewURL(slug string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/s/%s", s.httpPort, slug)
}

const channelConsumer = "channel"

// channelPollWindow is both how recent a channel resolve poll must be to count
// as presence, and the boot grace during which handleStart keeps checking.
const channelPollWindow = 3 * time.Second

// channelActive reports whether this window's channel consumer is wired to
// this review: attached to its SSE stream or recently polling resolve for this
// repo. The pid key is what keeps window A's polls from lighting up window B's
// start. The channel server polls every second from session start, so on a
// long-running daemon the answer is immediate. A daemon cold-booted by this
// very start has no poll history yet, so keep checking until the boot grace
// window closes — the channel's next 1s poll tick lands inside it. Never block
// resolve on this: the channel's own poll is the signal source.
func (s *Server) channelActive(ctx context.Context, reviewID, repoRoot string, pid int) bool {
	for {
		if s.activity.Attached(reviewID, channelConsumer, pid) ||
			s.activity.PolledSince(repoRoot, channelConsumer, pid, channelPollWindow) {
			return true
		}
		if time.Since(s.startedAt) >= channelPollWindow {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Server) handleResolve(ctx context.Context, req Request) Response {
	repoRoot, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return errResp(err.Error())
	}
	if req.Consumer != "" {
		s.activity.NotePoll(repoRoot, req.Consumer, req.ClaudePID)
	}
	resp := Response{OK: true, HTTPPort: s.httpPort}
	review, ok, err := s.resolver.Find(ctx, win(req), repoRoot)
	if err != nil {
		return errResp(err.Error())
	}
	if ok {
		resp.ReviewID = review.ID
		resp.Status = review.Status
	}
	return resp
}

func (s *Server) handleReply(ctx context.Context, req Request) Response {
	for _, in := range req.Replies {
		if in.AnswerTo != 0 {
			if resp := s.handleAnswer(ctx, in); !resp.OK {
				return resp
			}
			continue
		}
		if in.CommentID == 0 {
			return errResp("reply requires comment_id or answer_to")
		}
		if err := validateReplyKind(in); err != nil {
			return errResp(err.Error())
		}
		reviewID, versionNumber, err := s.store.ResolveCommentContext(ctx, in.CommentID)
		if err != nil {
			return errResp(err.Error())
		}
		// Hash the daemon's own re-marshal of Ask, never the client's raw JSON,
		// so semantically identical asks dedup regardless of key order.
		askJSON := ""
		if in.Ask != nil {
			b, err := json.Marshal(in.Ask)
			if err != nil {
				return errResp(fmt.Sprintf("encode ask: %v", err))
			}
			askJSON = string(b)
		}
		dedup := in.DedupKey
		if dedup == "" {
			dedup = deriveDedup(in.CommentID, in.Kind, in.Body, askJSON)
		}
		rid, inserted, err := s.store.CreateReply(ctx, store.Reply{
			CommentID: in.CommentID, Origin: store.OriginClaude, Kind: in.Kind, Body: in.Body,
			Ask: in.Ask, DedupKey: dedup,
		})
		if err != nil {
			return errResp(err.Error())
		}
		if !inserted {
			continue // a redelivered duplicate; do not re-emit
		}
		// Re-read the persisted row so the frame carries the stored created_at
		// (and can never drift from a later fetch of the same reply).
		r, err := s.store.GetReply(ctx, rid)
		if err != nil {
			return errResp(err.Error())
		}
		s.emitReply(ctx, reviewID, claudeEventType(in.Kind), versionNumber, in.CommentID, r)
	}
	return Response{OK: true}
}

// handleAnswer records a post-submit drain answer against a question or ask
// reply, then emits comment.updated so an open browser flips the card to its
// answered state. Origin claude keeps the frame out of Claude's own stream.
func (s *Server) handleAnswer(ctx context.Context, in ReplyInput) Response {
	target, err := s.store.GetReply(ctx, in.AnswerTo)
	if err != nil {
		return errResp(err.Error())
	}
	switch target.Kind {
	case "ask":
		if in.AskAnswer == nil {
			return errResp(fmt.Sprintf("reply %d is an ask: answer with ask_answer (select/other/notes)", in.AnswerTo))
		}
		if err := s.store.AnswerAsk(ctx, in.AnswerTo, *in.AskAnswer, "askuserquestion"); err != nil {
			return errResp(err.Error())
		}
	case "question":
		if in.Answer == "" {
			return errResp(fmt.Sprintf("reply %d is a question: answer with answer text", in.AnswerTo))
		}
		if err := s.store.AnswerQuestion(ctx, in.AnswerTo, in.Answer, "askuserquestion"); err != nil {
			return errResp(err.Error())
		}
		// Mirror the web path's visible answer bubble: wire.Reply carries no
		// plain-answer text, so without a sibling row the drained answer would
		// be invisible in an open browser. Origin user — the human authored it.
		if _, _, err := s.store.CreateReply(ctx, store.Reply{
			CommentID: target.CommentID, Origin: store.OriginUser, Kind: "answer", Body: in.Answer,
			DedupKey: deriveDedup(target.CommentID, "answer", in.Answer, ""),
		}); err != nil {
			return errResp(err.Error())
		}
	default:
		return errResp(fmt.Sprintf("reply %d is kind %q: not answerable", in.AnswerTo, target.Kind))
	}
	reviewID, versionNumber, err := s.store.ResolveCommentContext(ctx, target.CommentID)
	if err != nil {
		return errResp(err.Error())
	}
	s.emitThread(ctx, reviewID, versionNumber, target.CommentID)
	return Response{OK: true}
}

func validateReplyKind(in ReplyInput) error {
	switch in.Kind {
	case "question", "clarification":
		if in.Ask != nil {
			return fmt.Errorf("kind %q does not take an ask payload", in.Kind)
		}
		return nil
	case "ask":
		if in.Ask == nil {
			return fmt.Errorf("kind ask requires an ask payload")
		}
		if in.Body == "" {
			return fmt.Errorf("kind ask requires a body (the question text)")
		}
		return in.Ask.Validate()
	default:
		return fmt.Errorf("unknown reply kind %q (want question | ask | clarification)", in.Kind)
	}
}

func (s *Server) handleFeedback(ctx context.Context, req Request) Response {
	review, ok, err := s.lookupReview(ctx, req)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		return errResp("no review for this session/repo")
	}
	v, ok, err := s.store.LatestVersion(ctx, review.ID)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		return errResp("review has no versions")
	}
	fbPath := paths.FeedbackPath(review.ID, v.VersionNumber)
	fb, err := feedback.Load(fbPath)
	if err != nil {
		return errResp("feedback not frozen yet (review not submitted): " + err.Error())
	}
	b, _ := json.Marshal(fb)
	return Response{OK: true, FeedbackPath: fbPath, Feedback: b}
}

func (s *Server) handleStatus(ctx context.Context, req Request) Response {
	resp := Response{OK: true, DaemonVersion: version.String(), HTTPPort: s.httpPort}
	if review, ok, err := s.lookupReview(ctx, req); err == nil && ok {
		resp.ReviewID = review.ID
		resp.Status = review.Status
	}
	return resp
}

func (s *Server) handleSessionRecord(ctx context.Context, req Request) Response {
	if req.Session == "" {
		return Response{OK: true}
	}
	// Session ids rotate (each resume/clear/compact is a new id), so rebind the
	// window's open review to the new session here — this is what keeps
	// guard-edit, feedback, and status working across rotation.
	repoRoot, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true} // not a repo: nothing to rebind
	}
	if err := s.resolver.Rebind(ctx, win(req), repoRoot); err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true}
}

func (s *Server) handleGuardEdit(ctx context.Context, req Request) Response {
	repoRoot, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true, Allow: true} // not a repo: nothing to guard
	}
	review, ok, err := s.resolver.Find(ctx, win(req), repoRoot)
	if err != nil {
		// Couldn't determine status: fail closed and make the failure visible
		// rather than silently permitting an edit that an open review should block.
		return Response{OK: true, Allow: false, Reason: "cc-review: could not read review status (" + err.Error() + "); blocking the edit to be safe. Try `cc-review status`, or `cc-review stop` to clear the daemon."}
	}
	if !ok {
		return Response{OK: true, Allow: true} // no review: nothing to guard
	}
	if review.Status == "open" {
		return Response{
			OK: true, Allow: false,
			Reason: "cc-review: an open review is awaiting your feedback — edits are blocked until you press Submit in the browser.",
		}
	}
	return Response{OK: true, Allow: true}
}

// --- helpers ---------------------------------------------------------------

func (s *Server) lookupReview(ctx context.Context, req Request) (store.Review, bool, error) {
	repoRoot, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return store.Review{}, false, err
	}
	return s.resolver.Find(ctx, win(req), repoRoot)
}

func (s *Server) emitReply(ctx context.Context, reviewID, typ string, version int, commentID int64, r store.Reply) {
	_, _ = s.AppendEvent(ctx, &store.Event{
		ReviewID: reviewID, Origin: store.OriginClaude, Type: typ, VersionNumber: version,
		Payload: wire.Event(typ, version, map[string]any{
			"commentId": strconv.FormatInt(commentID, 10), "reply": wire.ToReply(r),
		}),
	})
}

// emitThread re-reads a comment with its replies and emits comment.updated.
func (s *Server) emitThread(ctx context.Context, reviewID string, version int, commentID int64) {
	c, err := s.store.GetComment(ctx, commentID)
	if err != nil {
		return
	}
	replies, err := s.store.ListRepliesByComment(ctx, commentID)
	if err != nil {
		return
	}
	_, _ = s.AppendEvent(ctx, &store.Event{
		ReviewID: reviewID, Origin: store.OriginClaude, Type: store.EventCommentUpdated, VersionNumber: version,
		Payload: wire.Event(store.EventCommentUpdated, version, map[string]any{
			"commentId": strconv.FormatInt(commentID, 10), "comment": wire.ToComment(c, replies),
		}),
	})
}

func errResp(msg string) Response { return Response{OK: false, Error: msg} }

func claudeEventType(kind string) string {
	switch kind {
	case "question":
		return store.EventClaudeQuestion
	case "ask":
		return store.EventClaudeAsk
	default:
		return store.EventClaudeClarification
	}
}

func deriveDedup(commentID int64, kind, body, askJSON string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{strconv.FormatInt(commentID, 10), kind, body, askJSON}, "\x00")))
	return hex.EncodeToString(h[:])
}
