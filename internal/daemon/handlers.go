package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yasyf/cc-review/internal/feedback"
	"github.com/yasyf/cc-review/internal/gitdiff"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/session"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/version"
	"github.com/yasyf/cc-review/internal/wire"
)

func (s *Server) handleStart(ctx context.Context, req Request) Response {
	if req.Cwd == "" {
		return errResp("start requires --cwd")
	}
	snap, err := gitdiff.Capture(ctx, req.Cwd)
	if err != nil {
		return errResp(err.Error())
	}
	review, resumed, err := session.Resolve(ctx, s.store, session.Opts{
		SessionID: req.Session, RepoRoot: snap.RepoRoot, New: req.New,
	})
	if err != nil {
		return errResp(err.Error())
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
	url := fmt.Sprintf("http://127.0.0.1:%d/s/%s?t=%s", s.httpPort, review.ID, s.token)
	return Response{
		OK: true, URL: url, ReviewID: review.ID, Version: v.VersionNumber, Resumed: resumed,
		HTTPPort: s.httpPort, Token: s.token,
	}
}

func (s *Server) handleResolve(ctx context.Context, req Request) Response {
	repoRoot, err := gitdiff.RepoRoot(ctx, req.Cwd)
	if err != nil {
		return errResp(err.Error())
	}
	resp := Response{OK: true, HTTPPort: s.httpPort, Token: s.token}
	review, ok, err := s.store.FindReviewBySessionRepo(ctx, req.Session, repoRoot)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		// A stream consumer may hold a sibling session's id — MCP servers are
		// spawned once and outlive session rotation — so fall back to the
		// repo's latest open review.
		review, ok, err = s.store.FindLatestOpenReviewByRepo(ctx, repoRoot)
		if err != nil {
			return errResp(err.Error())
		}
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
			if err := s.store.AnswerReply(ctx, in.AnswerTo, in.Answer, "askuserquestion"); err != nil {
				return errResp(err.Error())
			}
			continue
		}
		if in.CommentID == 0 {
			return errResp("reply requires comment_id or answer_to")
		}
		reviewID, versionNumber, err := s.store.ResolveCommentContext(ctx, in.CommentID)
		if err != nil {
			return errResp(err.Error())
		}
		kind := normalizeKind(in.Kind)
		optsJSON := "[]"
		if len(in.Options) > 0 {
			b, _ := json.Marshal(in.Options)
			optsJSON = string(b)
		}
		dedup := in.DedupKey
		if dedup == "" {
			dedup = deriveDedup(in.CommentID, kind, in.Body, optsJSON)
		}
		rid, inserted, err := s.store.CreateReply(ctx, store.Reply{
			CommentID: in.CommentID, Origin: store.OriginClaude, Kind: kind, Body: in.Body,
			OptionsJSON: optsJSON, DedupKey: dedup,
		})
		if err != nil {
			return errResp(err.Error())
		}
		if !inserted {
			continue // a redelivered duplicate; do not re-emit
		}
		r := store.Reply{
			ID: rid, CommentID: in.CommentID, Origin: store.OriginClaude, Kind: kind,
			Body: in.Body, OptionsJSON: optsJSON, CreatedAt: time.Now(),
		}
		typ := claudeEventType(kind)
		s.emitReply(ctx, reviewID, typ, versionNumber, in.CommentID, r)
	}
	return Response{OK: true}
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
	var started time.Time
	if req.StartedAt != 0 {
		started = time.Unix(req.StartedAt, 0)
	}
	err := s.store.UpsertSessionHook(ctx, store.SessionHook{
		SessionID: req.Session, Cwd: req.Cwd, TranscriptPath: req.TranscriptPath, StartedAt: started,
	})
	if err != nil {
		return errResp(err.Error())
	}
	// Session ids rotate (each resume/continue is a new id), so rebind the repo's
	// open review to the new session here — this is what keeps guard-edit,
	// feedback, and status (all exact-session matches) working across rotation.
	repoRoot, err := gitdiff.RepoRoot(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true} // not a repo: nothing to rebind
	}
	if _, bound, err := s.store.FindReviewBySessionRepo(ctx, req.Session, repoRoot); err != nil {
		return errResp(err.Error())
	} else if bound {
		return Response{OK: true} // this session already owns a review here
	}
	review, ok, err := s.store.FindLatestOpenReviewByRepo(ctx, repoRoot)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		return Response{OK: true}
	}
	if err := s.store.ReparentReviewSession(ctx, review.ID, req.Session, "session-start"); err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true}
}

func (s *Server) handleGuardEdit(ctx context.Context, req Request) Response {
	repoRoot, err := gitdiff.RepoRoot(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true, Allow: true} // not a repo: nothing to guard
	}
	review, ok, err := s.store.FindReviewBySessionRepo(ctx, req.Session, repoRoot)
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
	repoRoot, err := gitdiff.RepoRoot(ctx, req.Cwd)
	if err != nil {
		return store.Review{}, false, err
	}
	return s.store.FindReviewBySessionRepo(ctx, req.Session, repoRoot)
}

func (s *Server) emitReply(ctx context.Context, reviewID, typ string, version int, commentID int64, r store.Reply) {
	_, _ = s.AppendEvent(ctx, &store.Event{
		ReviewID: reviewID, Origin: store.OriginClaude, Type: typ, VersionNumber: version,
		Payload: wire.Event(typ, version, map[string]any{
			"commentId": strconv.FormatInt(commentID, 10), "reply": wire.ToReply(r),
		}),
	})
}

func errResp(msg string) Response { return Response{OK: false, Error: msg} }

func normalizeKind(kind string) string {
	switch kind {
	case "question", "option", "clarification":
		return kind
	default:
		return "clarification"
	}
}

func claudeEventType(kind string) string {
	switch kind {
	case "question":
		return store.EventClaudeQuestion
	case "option":
		return store.EventClaudeOption
	default:
		return store.EventClaudeClarification
	}
}

func deriveDedup(commentID int64, kind, body, optsJSON string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{strconv.FormatInt(commentID, 10), kind, body, optsJSON}, "\x00")))
	return hex.EncodeToString(h[:])
}
