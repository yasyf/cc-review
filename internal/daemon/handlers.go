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
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	// The repo lock spans both the patch capture and attributeVersion's tree
	// snapshot, so they describe the same working tree; turn-start/turn-end
	// snapshot under the same lock.
	hc.RepoLock.Lock()
	defer hc.RepoLock.Unlock()
	// Capture before any resolver write: a failed (e.g. empty) snapshot must
	// create nothing — and must not let --new close the prior review.
	var capture captured
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
		if !metaOK || (!meta.Stack && meta.BaseRef == "") {
			return errReply(fmt.Sprintf("review %s predates pinned diff bases; pass --new to start a fresh review", peeked.Slug))
		}
		if b.Base != "" {
			return errReply(fmt.Sprintf("review %s is pinned; pass --new to start a fresh review with --base", peeked.Slug))
		}
		fromPin = true
		if capture, err = captureForResume(hc, meta); err != nil {
			if !errors.Is(err, vcs.ErrNoChanges) {
				return errReply(err.Error() + " (pass --new to start a fresh review)")
			}
			return errReply(err.Error())
		}
	} else {
		if capture, err = captureForCreate(hc, b.Base); err != nil {
			return errReply(err.Error())
		}
	}
	slug := store.ReviewSlug(store.NewSlugHash())
	sub, resumed, err := hc.Subjects.Start(hc.Ctx, hc.Window, hc.Scope, slug, lifecycle, b.New)
	if err != nil {
		return errReply(err.Error())
	}
	// The resolve's verdict can flip under a concurrent rebind or submit between
	// the read and the write phase; re-align the capture with the review Start
	// actually returned.
	if resumed {
		meta, metaOK, err := st.GetReviewMeta(hc.Ctx, sub.ID)
		if err != nil {
			return errReply(err.Error())
		}
		// The resolve said create (so --base passed the gate above) but Start
		// resumed an existing pinned review: the explicit base cannot apply.
		if b.Base != "" {
			return errReply(fmt.Sprintf("review %s is pinned; pass --new to start a fresh review with --base", sub.Slug))
		}
		if metaOK && (meta.Stack != capture.Stack || (!meta.Stack && meta.BaseRef != capture.BaseRef)) {
			if capture, err = captureForResume(hc, meta); err != nil {
				return errReply(err.Error())
			}
		}
	} else {
		if fromPin {
			// The resolve said resume but Start created: recapture with create
			// semantics and re-pin the just-created (still version-less) review.
			if capture, err = captureForCreate(hc, ""); err != nil {
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
		if err := st.SetReviewMeta(hc.Ctx, sub.ID, capture.BaseRef, capture.Branch, capture.Stack); err != nil {
			return errReply(err.Error())
		}
	}
	// An unchanged worktree on resume reuses the latest version. A section whose
	// patch is unreadable (crash mid-rename) misses the dedup and gets a fresh one.
	if resumed {
		if latest, ok, err := st.LatestVersion(hc.Ctx, sub.ID); err != nil {
			return errReply(err.Error())
		} else if ok {
			existing, err := st.ListSections(hc.Ctx, latest.ID)
			if err != nil {
				return errReply(err.Error())
			}
			if sectionsUnchanged(existing, capture) {
				return rv.reuseVersion(hc, st, sub, latest, existing)
			}
		}
	}
	if err := paths.EnsureReviewDir(sub.ID); err != nil {
		return errReply(err.Error())
	}
	sectionInputs := make([]store.SectionInput, len(capture.Sections))
	for i, sec := range capture.Sections {
		classified := generated.Classify(hc.Ctx, capture.RepoRoot, sec.Files)
		cfiles := make([]store.ClassifiedFile, len(sec.Files))
		for j, f := range sec.Files {
			flags := classified[f.Path]
			cfiles[j] = store.ClassifiedFile{FileChange: f, Generated: flags.Generated, Vendored: flags.Vendored}
		}
		filesJSON, err := json.Marshal(cfiles)
		if err != nil {
			return errReply(err.Error())
		}
		sectionInputs[i] = store.SectionInput{
			Position: i, Branch: sec.Branch, ParentBranch: sec.ParentBranch,
			BaseRef: sec.BaseRef, HeadRef: sec.HeadRef, Pending: sec.Pending, FilesJSON: string(filesJSON),
		}
	}
	// Patch each section to a temp before the version insert; a failed write then
	// leaves no committed-but-unreadable section. Final paths land via rename below.
	tmps := make([]string, len(capture.Sections))
	for i, sec := range capture.Sections {
		tmp, err := os.CreateTemp(paths.ReviewDir(sub.ID), "snap-*.tmp")
		if err != nil {
			removeAll(tmps)
			return errReply(err.Error())
		}
		tmps[i] = tmp.Name()
		if _, err := tmp.WriteString(sec.PatchText); err != nil {
			_ = tmp.Close()
			removeAll(tmps)
			return errReply(err.Error())
		}
		if err := tmp.Close(); err != nil {
			removeAll(tmps)
			return errReply(err.Error())
		}
	}
	v, sections, err := st.CreateVersion(hc.Ctx, sub.ID, capture.Branch, capture.BaseRef, sub.SessionID, sectionInputs)
	if err != nil {
		removeAll(tmps)
		return errReply(err.Error())
	}
	for _, sec := range sections {
		patchPath := paths.SectionSnapshotPath(sub.ID, v.VersionNumber, sec.Position)
		if err := os.Rename(tmps[sec.Position], patchPath); err != nil {
			removeAll(tmps)
			return errReply(err.Error())
		}
		if err := st.UpdateSectionPatchPath(hc.Ctx, sec.ID, patchPath); err != nil {
			removeAll(tmps)
			return errReply(err.Error())
		}
	}
	// Attribution holds only on the pending section; a clean stack has none.
	for _, sec := range sections {
		if sec.Pending {
			rv.attributeVersion(hc.Ctx, st, capture.RepoRoot, sec.ID, capture.Sections[sec.Position].PatchText)
			break
		}
	}
	// A new version reopens the review (a prior round may have been submitted), so
	// the edit guard blocks edits again until this round is submitted.
	if sub.Status != statusOpen {
		if err := hc.Subjects.Store.SetStatus(hc.Ctx, sub.ID, statusOpen); err != nil {
			return errReply(err.Error())
		}
		emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginSystem, store.EventStatusChanged,
			v.VersionNumber, map[string]any{"status": statusOpen})
	}
	// The carry upsert lands before version.created (the SPA refetches on it),
	// then version.created, then the unmark batch.
	fingerprints := make(map[store.SectionFileKey]string)
	for i, sec := range sections {
		for _, f := range capture.Sections[i].Files {
			fingerprints[store.SectionFileKey{SectionKey: sec.Key(), Path: f.Path}] = f.Fingerprint
		}
	}
	carried, allCarried, err := rv.carrySectionOrganizations(hc.Ctx, st, sub.ID, sections)
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
			states = append(states, map[string]any{"sectionKey": fs.SectionKey, "path": fs.Path, "reviewed": false, "hidden": fs.Hidden})
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
	// A carried organization is the agent's own content reattached, so its event
	// keeps agent origin (the channel stream filters agent-origin frames).
	for _, sec := range sections {
		if org, ok := carried[sec.ID]; ok {
			emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginAgent, store.EventOrganizationUpdated, v.VersionNumber,
				map[string]any{"sectionKey": sec.Key(), "organization": org})
		}
	}
	if allCarried {
		if err := closeStaleOrganizeRequests(hc.Ctx, st, hc.Append, sub.ID, v.VersionNumber,
			fmt.Sprintf("diff unchanged; organization carried to version %d", v.VersionNumber)); err != nil {
			return errReply(err.Error())
		}
	} else {
		organize, err := st.CreateAIRequest(hc.Ctx, sub.ID, v.VersionNumber, store.OriginSystem, organizePrompt)
		if err != nil {
			return errReply(err.Error())
		}
		emitAIRequest(hc.Ctx, hc.Append, ccevent.OriginSystem, store.EventAIRequestCreated, v.VersionNumber, organize)
	}
	reoffer, err := openAIRequestsJSON(hc.Ctx, st, sub.ID, v.VersionNumber)
	if err != nil {
		return errReply(err.Error())
	}
	return rv.startReply(hc, sub, v.VersionNumber, resumed, cs, stackInfoFor(capture), reoffer)
}

// reuseVersion handles a resume whose capture matches the latest version
// byte-for-byte: it reopens a submitted round and re-offers open requests
// without minting a new version.
func (rv *review) reuseVersion(hc ccd.HandlerCtx, st *store.Store, sub subject.Subject, latest store.Version, sections []store.Section) ccd.Reply {
	if sub.Status != statusOpen {
		if err := hc.Subjects.Store.SetStatus(hc.Ctx, sub.ID, statusOpen); err != nil {
			return errReply(err.Error())
		}
		emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginSystem, store.EventStatusChanged,
			latest.VersionNumber, map[string]any{"status": statusOpen})
	}
	cs := rv.channelStateProbed(hc, sub.ID)
	// Every section organized: close any stranded organize request. Otherwise
	// rescue an open one or queue a fresh request.
	organized, err := allSectionsOrganized(hc.Ctx, st, sections)
	if err != nil {
		return errReply(err.Error())
	}
	if organized {
		if err := closeStaleOrganizeRequests(hc.Ctx, st, hc.Append, sub.ID, latest.VersionNumber,
			fmt.Sprintf("diff unchanged; version %d is already organized", latest.VersionNumber)); err != nil {
			return errReply(err.Error())
		}
	} else if _, found, err := openSystemOrganize(hc.Ctx, st, sub.ID); err != nil {
		return errReply(err.Error())
	} else if !found {
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
	return rv.startReply(hc, sub, latest.VersionNumber, true, cs, nil, reoffer)
}

// startReply builds the start op's reply: the review id and http port on the
// envelope, the URL, version, resume flag, channel state, stack info, and
// re-offered AI requests in the body.
func (rv *review) startReply(hc ccd.HandlerCtx, sub subject.Subject, version int, resumed bool, channelState string, stack *StackInfo, aiRequests []json.RawMessage) ccd.Reply {
	raw, _ := json.Marshal(result{
		URL: reviewURL(hc.HTTPPort, sub.Slug), Version: version, Resumed: resumed,
		ChannelState: channelState, Stack: stack, AIRequests: aiRequests,
	})
	return ccd.Reply{OK: true, SubjectID: sub.ID, HTTPPort: hc.HTTPPort, Body: raw}
}

// captured is one working-tree or stack snapshot normalized into ordered
// sections. Stack marks a Graphite capture; a flat capture is one pending
// section. BaseRef pins a flat review's base; a stack pins none.
type captured struct {
	RepoRoot string
	Branch   string
	BaseRef  string
	Trunk    string
	Stack    bool
	Sections []vcs.StackSection
}

// captureForResume re-captures a review from its pinned meta: a stack re-detects
// its sections from current refs; a flat review diffs its pinned base verbatim.
func captureForResume(hc ccd.HandlerCtx, meta store.ReviewMeta) (captured, error) {
	if meta.Stack {
		snap, err := vcs.CaptureStack(hc.Ctx, hc.Scope)
		if err != nil {
			return captured{}, err
		}
		return stackToCaptured(snap), nil
	}
	snap, err := vcs.CaptureAt(hc.Ctx, hc.Scope, meta.BaseRef)
	if err != nil {
		return captured{}, err
	}
	return flatToCaptured(snap), nil
}

// captureForCreate captures a fresh review: an explicit --base forces a flat
// diff; otherwise a Graphite stack auto-detects and captures per-branch, falling
// back to a flat session-scoped diff.
func captureForCreate(hc ccd.HandlerCtx, base string) (captured, error) {
	if base == "" {
		if _, stacked, err := vcs.DetectStack(hc.Ctx, hc.Scope); err != nil {
			return captured{}, err
		} else if stacked {
			snap, err := vcs.CaptureStack(hc.Ctx, hc.Scope)
			if err != nil {
				return captured{}, err
			}
			return stackToCaptured(snap), nil
		}
	}
	snap, err := vcs.Capture(hc.Ctx, hc.Scope, base)
	if err != nil {
		return captured{}, err
	}
	return flatToCaptured(snap), nil
}

// flatToCaptured normalizes a flat snapshot into one pending section.
func flatToCaptured(snap vcs.Snapshot) captured {
	return captured{
		RepoRoot: snap.RepoRoot, Branch: snap.Branch, BaseRef: snap.BaseRef, Stack: false,
		Sections: []vcs.StackSection{{
			Branch: snap.Branch, BaseRef: snap.BaseRef,
			PatchText: snap.PatchText, Files: snap.Files, Pending: true,
		}},
	}
}

func stackToCaptured(snap vcs.StackSnapshot) captured {
	return captured{
		RepoRoot: snap.RepoRoot, Branch: snap.Branch, Trunk: snap.Trunk, Stack: true,
		Sections: snap.Sections,
	}
}

// stackInfoFor is the start reply's stack summary: trunk plus committed branches
// trunk-most→top, nil for a flat capture.
func stackInfoFor(capture captured) *StackInfo {
	if !capture.Stack {
		return nil
	}
	branches := make([]string, 0, len(capture.Sections))
	for _, sec := range capture.Sections {
		if !sec.Pending {
			branches = append(branches, sec.Branch)
		}
	}
	return &StackInfo{Trunk: capture.Trunk, Branches: branches}
}

// sectionsUnchanged reports whether an existing version matches a fresh capture
// position-for-position: same branch, parent, pending, and patch bytes (refs
// excluded — a no-op restack rewrites shas, not diffs).
func sectionsUnchanged(existing []store.Section, capture captured) bool {
	if len(existing) != len(capture.Sections) {
		return false
	}
	for i, sec := range existing {
		want := capture.Sections[i]
		if sec.Branch != want.Branch || sec.ParentBranch != want.ParentBranch || sec.Pending != want.Pending {
			return false
		}
		prev, err := os.ReadFile(sec.PatchPath)
		if err != nil || string(prev) != want.PatchText {
			return false
		}
	}
	return true
}

// removeAll discards temp patch files after a failed create.
func removeAll(names []string) {
	for _, n := range names {
		if n != "" {
			_ = os.Remove(n)
		}
	}
}

func (rv *review) handleReply(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
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
