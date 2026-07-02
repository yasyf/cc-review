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

	ccd "github.com/yasyf/cc-interact/daemon"
	ccevent "github.com/yasyf/cc-interact/event"
	"github.com/yasyf/cc-interact/subject"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/feedback"
	"github.com/yasyf/cc-review/internal/generated"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

func (rv *review) handleStart(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b := decodeBody(hc.Env.Body)
	// The repo lock spans both the patch capture and attributeVersion's tree
	// snapshot, so they describe the same working tree; turn-start/turn-end
	// snapshot under the same lock.
	hc.RepoLock.Lock()
	defer hc.RepoLock.Unlock()
	// Capture before any resolver write: a failed (e.g. empty) snapshot must
	// create nothing — and must not let --new close the prior review. A resumed
	// review captures against its pinned base, so the resolve comes first.
	var snap vcs.Snapshot
	fromPin := false
	peeked, ok, err := hc.Subjects.Find(hc.Ctx, hc.Window, hc.Scope)
	if err != nil {
		return errReply(err.Error())
	}
	if ok && !b.New {
		meta, metaOK, err := st.GetReviewMeta(hc.Ctx, peeked.ID)
		if err != nil {
			return errReply(err.Error())
		}
		if !metaOK || meta.BaseRef == "" {
			return errReply(fmt.Sprintf("review %s predates pinned diff bases; pass --new to start a fresh review", peeked.Slug))
		}
		if b.Base != "" {
			return errReply(fmt.Sprintf("review %s is pinned to base %s; pass --new to start a fresh review with --base", peeked.Slug, meta.BaseRef))
		}
		fromPin = true
		if snap, err = vcs.CaptureAt(hc.Ctx, hc.Scope, meta.BaseRef); err != nil {
			if !errors.Is(err, vcs.ErrNoChanges) {
				return errReply(err.Error() + " (pass --new to start a fresh review)")
			}
			return errReply(err.Error())
		}
	} else {
		if snap, err = vcs.Capture(hc.Ctx, hc.Scope, b.Base); err != nil {
			return errReply(err.Error())
		}
	}
	slug := store.ReviewSlug(snap.Branch, store.NewSlugHash())
	sub, resumed, err := hc.Subjects.Start(hc.Ctx, hc.Window, hc.Scope, slug, lifecycle, b.New)
	if err != nil {
		return errReply(err.Error())
	}
	// The resolve's verdict can flip under a concurrent rebind or submit between
	// the read and the write phase; re-align the snapshot with the review Start
	// actually returned.
	if resumed {
		meta, metaOK, err := st.GetReviewMeta(hc.Ctx, sub.ID)
		if err != nil {
			return errReply(err.Error())
		}
		// The resolve said create (so --base passed the gate above) but Start
		// resumed an existing pinned review: the explicit base cannot apply.
		if b.Base != "" {
			return errReply(fmt.Sprintf("review %s is pinned to base %s; pass --new to start a fresh review with --base", sub.Slug, meta.BaseRef))
		}
		if metaOK && meta.BaseRef != snap.BaseRef {
			if snap, err = vcs.CaptureAt(hc.Ctx, hc.Scope, meta.BaseRef); err != nil {
				return errReply(err.Error())
			}
		}
	} else {
		if fromPin {
			// The resolve said resume but Start created: the snapshot was taken
			// against the vanished review's pin; recapture with create semantics and
			// re-pin the just-created (still version-less) review.
			if snap, err = vcs.Capture(hc.Ctx, hc.Scope, ""); err != nil {
				// Leave nothing resumable behind: the empty review would otherwise be
				// resumed against its stale pin on the next start.
				if cerr := hc.Subjects.Store.SetStatus(hc.Ctx, sub.ID, "closed"); cerr != nil {
					return errReply(cerr.Error())
				}
				if derr := hc.Subjects.Store.Detach(hc.Ctx, sub.ID); derr != nil {
					return errReply(derr.Error())
				}
				return errReply(err.Error())
			}
		}
		if err := st.SetReviewMeta(hc.Ctx, sub.ID, snap.BaseRef, snap.Branch); err != nil {
			return errReply(err.Error())
		}
	}
	// An unchanged worktree on resume reuses the latest version instead of
	// stacking an identical one and re-queueing an organize request. A version
	// whose patch file is unreadable (crash between insert and rename) just misses
	// the dedup and gets a fresh version.
	if resumed {
		if latest, ok, err := st.LatestVersion(hc.Ctx, sub.ID); err != nil {
			return errReply(err.Error())
		} else if ok {
			if prev, err := os.ReadFile(latest.PatchPath); err == nil && string(prev) == snap.PatchText {
				// A successful start always leaves the round open: resuming a submitted
				// review must re-block edits even when the snapshot is unchanged. The
				// status.changed unfreezes any browser tab still showing the old state.
				if sub.Status != statusOpen {
					if err := hc.Subjects.Store.SetStatus(hc.Ctx, sub.ID, statusOpen); err != nil {
						return errReply(err.Error())
					}
					emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginSystem, store.EventStatusChanged,
						latest.VersionNumber, map[string]any{"status": statusOpen})
				}
				cs := rv.channelStateProbed(hc, sub.ID)
				// An organized version needs no organize agent: close any open system
				// request stranded by a dead session. An unorganized one rescues the
				// still-open request — or queues a fresh one when the prior request
				// finished without organizing.
				if _, ok, err := st.GetOrganization(hc.Ctx, latest.ID); err != nil {
					return errReply(err.Error())
				} else if ok {
					if err := closeStaleOrganizeRequests(hc.Ctx, st, hc.Append, sub.ID, latest.VersionNumber,
						fmt.Sprintf("diff unchanged; version %d is already organized", latest.VersionNumber)); err != nil {
						return errReply(err.Error())
					}
					reoffer, err := openAIRequestsJSON(hc.Ctx, st, sub.ID, latest.VersionNumber)
					if err != nil {
						return errReply(err.Error())
					}
					return rv.startReply(hc, sub, latest.VersionNumber, true, cs, reoffer)
				}
				_, found, err := openSystemOrganize(hc.Ctx, st, sub.ID)
				if err != nil {
					return errReply(err.Error())
				}
				if !found {
					ar, err := st.CreateAIRequest(hc.Ctx, sub.ID, latest.VersionNumber, store.OriginSystem, organizePrompt)
					if err != nil {
						return errReply(err.Error())
					}
					emitAIRequest(hc.Ctx, hc.Append, ccevent.OriginSystem, store.EventAIRequestCreated, latest.VersionNumber, ar)
				}
				reoffer, err := openAIRequestsJSON(hc.Ctx, st, sub.ID, latest.VersionNumber)
				if err != nil {
					return errReply(err.Error())
				}
				return rv.startReply(hc, sub, latest.VersionNumber, true, cs, reoffer)
			}
		}
	}
	if err := paths.EnsureReviewDir(sub.ID); err != nil {
		return errReply(err.Error())
	}
	classified := generated.Classify(hc.Ctx, snap.RepoRoot, snap.Files)
	cfiles := make([]store.ClassifiedFile, len(snap.Files))
	for i, f := range snap.Files {
		flags := classified[f.Path]
		cfiles[i] = store.ClassifiedFile{FileChange: f, Generated: flags.Generated, Vendored: flags.Vendored}
	}
	filesJSON, err := json.Marshal(cfiles)
	if err != nil {
		return errReply(err.Error())
	}
	// Write the patch to a temp file before inserting the version row, so a write
	// failure can never leave behind a committed-but-unreadable version. The row
	// then gets the final path after an atomic rename into place.
	tmp, err := os.CreateTemp(paths.ReviewDir(sub.ID), "snap-*.tmp")
	if err != nil {
		return errReply(err.Error())
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(snap.PatchText); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return errReply(err.Error())
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return errReply(err.Error())
	}
	v, err := st.CreateVersion(hc.Ctx, sub.ID, snap.Branch, snap.BaseRef, "", string(filesJSON), sub.SessionID)
	if err != nil {
		_ = os.Remove(tmpName)
		return errReply(err.Error())
	}
	patchPath := paths.SnapshotPath(sub.ID, v.VersionNumber)
	if err := os.Rename(tmpName, patchPath); err != nil {
		_ = os.Remove(tmpName)
		return errReply(err.Error())
	}
	if err := st.UpdateVersionPatchPath(hc.Ctx, v.ID, patchPath); err != nil {
		return errReply(err.Error())
	}
	rv.attributeVersion(hc.Ctx, st, hc.Scope, v.ID, snap.PatchText)
	// A new version reopens the review (a prior round may have been submitted), so
	// the edit guard blocks edits again until this round is submitted. The
	// status.changed unfreezes any browser tab still showing the old state.
	if sub.Status != statusOpen {
		if err := hc.Subjects.Store.SetStatus(hc.Ctx, sub.ID, statusOpen); err != nil {
			return errReply(err.Error())
		}
		emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginSystem, store.EventStatusChanged,
			v.VersionNumber, map[string]any{"status": statusOpen})
	}
	// Carry review state across versions: unmark files whose diff content changed
	// (version.created first, then the unmark batch), then queue the system
	// organize request for the live Claude session.
	fingerprints := make(map[string]string, len(snap.Files))
	for _, f := range snap.Files {
		fingerprints[f.Path] = f.Fingerprint
	}
	// The carry upsert lands before the version.created append: the SPA refetches
	// the session on that event, and the refetch must already see the new
	// version's organization row.
	carried, carriedOK, err := carryOrganizationForward(hc.Ctx, st, sub.ID, v, fingerprints)
	if err != nil {
		return errReply(err.Error())
	}
	unmarked, err := st.UnreviewChangedFiles(hc.Ctx, sub.ID, fingerprints)
	if err != nil {
		return errReply(err.Error())
	}
	emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginSystem, store.EventVersionCreated, v.VersionNumber, nil)
	if len(unmarked) > 0 {
		states := make([]map[string]any, 0, len(unmarked))
		for _, fs := range unmarked {
			states = append(states, map[string]any{"path": fs.Path, "reviewed": false, "hidden": fs.Hidden})
		}
		emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginSystem, store.EventFileStates, v.VersionNumber,
			map[string]any{"states": states})
	}
	// A question parked on a now-superseded version can never be re-offered or
	// answered, so fail it rather than leave the chip lit forever.
	if err := failStrandedQuestions(hc.Ctx, st, hc.Append, sub.ID, v.VersionNumber); err != nil {
		return errReply(err.Error())
	}
	cs := rv.channelStateProbed(hc, sub.ID)
	if carriedOK {
		// The carried organization is the agent's own authored content reattached
		// verbatim, so the event keeps the agent origin a submit_organization would
		// have — the channel stream filters agent-origin frames, and no organize
		// agent is dispatched for it.
		emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginAgent, store.EventOrganizationUpdated, v.VersionNumber,
			map[string]any{"organization": carried})
		if err := closeStaleOrganizeRequests(hc.Ctx, st, hc.Append, sub.ID, v.VersionNumber,
			fmt.Sprintf("diff unchanged; organization carried to version %d", v.VersionNumber)); err != nil {
			return errReply(err.Error())
		}
		reoffer, err := openAIRequestsJSON(hc.Ctx, st, sub.ID, v.VersionNumber)
		if err != nil {
			return errReply(err.Error())
		}
		return rv.startReply(hc, sub, v.VersionNumber, resumed, cs, reoffer)
	}
	organize, err := st.CreateAIRequest(hc.Ctx, sub.ID, v.VersionNumber, store.OriginSystem, organizePrompt)
	if err != nil {
		return errReply(err.Error())
	}
	emitAIRequest(hc.Ctx, hc.Append, ccevent.OriginSystem, store.EventAIRequestCreated, v.VersionNumber, organize)
	reoffer, err := openAIRequestsJSON(hc.Ctx, st, sub.ID, v.VersionNumber)
	if err != nil {
		return errReply(err.Error())
	}
	return rv.startReply(hc, sub, v.VersionNumber, resumed, cs, reoffer)
}

// startReply builds the start op's reply: the review id and http port on the
// envelope, the URL, version, resume flag, channel state, and re-offered AI
// requests in the body.
func (rv *review) startReply(hc ccd.HandlerCtx, sub subject.Subject, version int, resumed bool, channelState string, aiRequests []json.RawMessage) ccd.Reply {
	raw, _ := json.Marshal(result{
		URL: reviewURL(hc.HTTPPort, sub.Slug), Version: version, Resumed: resumed,
		ChannelState: channelState, AIRequests: aiRequests,
	})
	return ccd.Reply{OK: true, SubjectID: sub.ID, HTTPPort: hc.HTTPPort, Body: raw}
}

func (rv *review) handleReply(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b := decodeBody(hc.Env.Body)
	for _, in := range b.Replies {
		if in.AnswerTo != 0 {
			if reply := rv.handleAnswer(hc, st, in); !reply.OK {
				return reply
			}
			continue
		}
		if in.CommentID == 0 {
			return errReply("reply requires comment_id or answer_to")
		}
		if err := validateReplyKind(in); err != nil {
			return errReply(err.Error())
		}
		reviewID, versionNumber, err := st.ResolveCommentContext(hc.Ctx, in.CommentID)
		if err != nil {
			return errReply(err.Error())
		}
		// Hash the daemon's own re-marshal of Ask, never the client's raw JSON, so
		// semantically identical asks dedup regardless of key order.
		askJSON := ""
		if in.Ask != nil {
			j, err := json.Marshal(in.Ask)
			if err != nil {
				return errReply(fmt.Sprintf("encode ask: %v", err))
			}
			askJSON = string(j)
		}
		dedup := in.DedupKey
		if dedup == "" {
			dedup = deriveDedup(in.CommentID, in.Kind, in.Body, askJSON)
		}
		rid, inserted, err := st.CreateReply(hc.Ctx, store.Reply{
			CommentID: in.CommentID, Origin: store.OriginClaude, Kind: in.Kind, Body: in.Body,
			Ask: in.Ask, DedupKey: dedup,
		})
		if err != nil {
			return errReply(err.Error())
		}
		if !inserted {
			continue // a redelivered duplicate; do not re-emit
		}
		// Re-read the persisted row so the frame carries the stored created_at (and
		// can never drift from a later fetch of the same reply).
		r, err := st.GetReply(hc.Ctx, rid)
		if err != nil {
			return errReply(err.Error())
		}
		emitReply(hc.Ctx, hc.Append, reviewID, claudeEventType(in.Kind), versionNumber, in.CommentID, r)
	}
	return ccd.Reply{OK: true}
}

// handleAnswer records a post-submit drain answer against a question or ask
// reply, then emits comment.updated so an open browser flips the card to its
// answered state. Origin agent keeps the frame out of Claude's own stream.
func (rv *review) handleAnswer(hc ccd.HandlerCtx, st *store.Store, in ReplyInput) ccd.Reply {
	target, err := st.GetReply(hc.Ctx, in.AnswerTo)
	if err != nil {
		return errReply(err.Error())
	}
	switch target.Kind {
	case "ask":
		if in.AskAnswer == nil {
			return errReply(fmt.Sprintf("reply %d is an ask: answer with ask_answer (select/other/notes)", in.AnswerTo))
		}
		if err := st.AnswerAsk(hc.Ctx, in.AnswerTo, *in.AskAnswer, "askuserquestion"); err != nil {
			return errReply(err.Error())
		}
	case "question":
		if in.Answer == "" {
			return errReply(fmt.Sprintf("reply %d is a question: answer with answer text", in.AnswerTo))
		}
		if err := st.AnswerQuestion(hc.Ctx, in.AnswerTo, in.Answer, "askuserquestion"); err != nil {
			return errReply(err.Error())
		}
		// Mirror the web path's visible answer bubble: wire.Reply carries no
		// plain-answer text, so without a sibling row the drained answer would be
		// invisible in an open browser. Origin user — the human authored it.
		if _, _, err := st.CreateReply(hc.Ctx, store.Reply{
			CommentID: target.CommentID, Origin: store.OriginUser, Kind: "answer", Body: in.Answer,
			DedupKey: deriveDedup(target.CommentID, "answer", in.Answer, ""),
		}); err != nil {
			return errReply(err.Error())
		}
	default:
		return errReply(fmt.Sprintf("reply %d is kind %q: not answerable", in.AnswerTo, target.Kind))
	}
	reviewID, versionNumber, err := st.ResolveCommentContext(hc.Ctx, target.CommentID)
	if err != nil {
		return errReply(err.Error())
	}
	emitThread(hc.Ctx, hc.Append, st, reviewID, versionNumber, target.CommentID)
	return ccd.Reply{OK: true}
}

func (rv *review) handleFeedback(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	sub, ok, err := hc.Subjects.Find(hc.Ctx, hc.Window, hc.Scope)
	if err != nil {
		return errReply(err.Error())
	}
	if !ok {
		return errReply("no review for this session/repo")
	}
	v, ok, err := st.LatestVersion(hc.Ctx, sub.ID)
	if err != nil {
		return errReply(err.Error())
	}
	if !ok {
		return errReply("review has no versions")
	}
	fbPath := paths.FeedbackPath(sub.ID, v.VersionNumber)
	fb, err := feedback.Load(fbPath)
	if err != nil {
		return errReply("feedback not frozen yet (review not submitted): " + err.Error())
	}
	raw, _ := json.Marshal(fb)
	return okReply(result{FeedbackPath: fbPath, Feedback: raw})
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

// emitReply appends a claude.* reply event under the agent origin.
func emitReply(ctx context.Context, ap ccd.AppendFunc, reviewID, typ string, version int, commentID int64, r store.Reply) {
	emit(ctx, ap, reviewID, ccevent.OriginAgent, typ, version, map[string]any{
		"commentId": strconv.FormatInt(commentID, 10), "reply": wire.ToReply(r),
	})
}

// emitThread re-reads a comment with its replies and emits comment.updated.
func emitThread(ctx context.Context, ap ccd.AppendFunc, st *store.Store, reviewID string, version int, commentID int64) {
	c, err := st.GetComment(ctx, commentID)
	if err != nil {
		return
	}
	replies, err := st.ListRepliesByComment(ctx, commentID)
	if err != nil {
		return
	}
	emit(ctx, ap, reviewID, ccevent.OriginAgent, store.EventCommentUpdated, version, map[string]any{
		"commentId": strconv.FormatInt(commentID, 10), "comment": wire.ToComment(c, replies),
	})
}

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
