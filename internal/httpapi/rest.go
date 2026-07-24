package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	ccevent "github.com/yasyf/cc-interact/event"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/corrections"
	"github.com/yasyf/cc-review/internal/feedback"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

// --- wire types ------------------------------------------------------------

type sessionResponse struct {
	Review          wire.Review                `json:"review"`
	Version         int                        `json:"version"`
	VersionID       string                     `json:"versionId"`
	Sections        []sectionResponse          `json:"sections"`
	Comments        []wire.Comment             `json:"comments"`
	Annotations     []wire.Annotation          `json:"annotations"`
	AIRequests      []wire.AIRequest           `json:"aiRequests"`
	Turns           []wire.Turn                `json:"turns"`
	TurnActivity    map[string][]wire.Decision `json:"turnActivity"`
	ClaudeConnected bool                       `json:"claudeConnected"`
	LatestEventSeq  string                     `json:"latestEventSeq"`
}

// sectionResponse is one section's diff and per-section state. Attributions are
// carried only on the pending section (the working tree).
type sectionResponse struct {
	SectionID    string                              `json:"sectionId"`
	SectionKey   string                              `json:"sectionKey"`
	Position     int                                 `json:"position"`
	Branch       string                              `json:"branch"`
	ParentBranch string                              `json:"parentBranch"`
	BaseRef      string                              `json:"baseRef"`
	HeadRef      string                              `json:"headRef"`
	Pending      bool                                `json:"pending"`
	Patch        string                              `json:"patchText"`
	Files        json.RawMessage                     `json:"files"`
	FileStates   map[string]wire.FileState           `json:"fileStates"`
	Organization *store.Organization                 `json:"organization"`
	Attributions *map[string][]wire.AttributionRange `json:"attributions,omitempty"`
}

type createCommentReq struct {
	SectionID string `json:"sectionId"`
	FilePath  string `json:"filePath"`
	Side      string `json:"side"`
	Range     struct {
		Start     int    `json:"start"`
		End       int    `json:"end"`
		StartSide string `json:"startSide"`
		EndSide   string `json:"endSide"`
	} `json:"range"`
	LineContent string `json:"lineContent"`
	Body        string `json:"body"`
}

type updateCommentReq struct {
	Status string  `json:"status"`
	Body   *string `json:"body"`
}

type createReplyReq struct {
	Body            string           `json:"body"`
	AskAnswer       *store.AskAnswer `json:"askAnswer"`
	QuestionReplyID string           `json:"questionReplyId"`
}

type submitReq struct {
	ReviewID string `json:"reviewId"`
}

type closeReq struct {
	ReviewID string `json:"reviewId"`
}

type fileStatesReq struct {
	ReviewID string `json:"reviewId"`
	Files    []struct {
		SectionKey string `json:"sectionKey"`
		Path       string `json:"path"`
		Reviewed   *bool  `json:"reviewed"`
		Hidden     *bool  `json:"hidden"`
	} `json:"files"`
}

type fileStateOut struct {
	SectionKey string `json:"sectionKey"`
	Path       string `json:"path"`
	Reviewed   bool   `json:"reviewed"`
	Hidden     bool   `json:"hidden"`
}

type createAIRequestReq struct {
	ReviewID string `json:"reviewId"`
	Prompt   string `json:"prompt"`
}

type answerAIRequestReq struct {
	Answer    string           `json:"answer"`
	AskAnswer *store.AskAnswer `json:"askAnswer"`
}

// --- handlers --------------------------------------------------------------

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	review, err := s.st().GetReviewByRef(ctx, r.PathValue("reviewId"))
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	var version store.Version
	if v := r.URL.Query().Get("version"); v != "" {
		n, _ := strconv.Atoi(v)
		version, err = s.st().GetVersion(ctx, review.ID, n)
	} else {
		var ok bool
		version, ok, err = s.st().LatestVersion(ctx, review.ID)
		if err == nil && !ok {
			err = store.ErrNotFound
		}
	}
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	sections, err := s.st().ListSections(ctx, version.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	states, err := s.st().ListFileStates(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	statesByKey := make(map[store.SectionFileKey]store.FileState, len(states))
	for _, st := range states {
		statesByKey[store.SectionFileKey{SectionKey: st.SectionKey, Path: st.Path}] = st
	}
	orgs, err := s.st().GetOrganizationsByVersion(ctx, version.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	keyByID := make(map[int64]string, len(sections))
	turnIDSet := make(map[int64]bool)
	sectionResp := make([]sectionResponse, 0, len(sections))
	for _, sec := range sections {
		keyByID[sec.ID] = sec.Key()
		patch, err := os.ReadFile(sec.PatchPath)
		if err != nil {
			http.Error(w, "read patch: "+err.Error(), http.StatusInternalServerError)
			return
		}
		files, err := sec.Files()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fileStates := make(map[string]wire.FileState)
		for _, f := range files {
			if st, ok := statesByKey[store.SectionFileKey{SectionKey: sec.Key(), Path: f.Path}]; ok {
				fileStates[f.Path] = wire.FileState{Reviewed: st.Reviewed, Hidden: st.Hidden}
			}
		}
		var organization *store.Organization
		if org, ok := orgs[sec.ID]; ok {
			organization = &org
		}
		// Attributions ride only on the pending section (the working tree); other
		// sections omit the key entirely.
		var attributions *map[string][]wire.AttributionRange
		if sec.Pending {
			attrsByFile, err := s.st().ListAttributionsBySection(ctx, sec.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			pending := make(map[string][]wire.AttributionRange, len(attrsByFile))
			for path, ranges := range attrsByFile {
				out := make([]wire.AttributionRange, 0, len(ranges))
				for _, rg := range ranges {
					out = append(out, wire.ToAttributionRange(rg))
					if rg.TurnID != 0 {
						turnIDSet[rg.TurnID] = true
					}
				}
				pending[path] = out
			}
			attributions = &pending
		}
		sectionResp = append(sectionResp, sectionResponse{
			SectionID: strconv.FormatInt(sec.ID, 10), SectionKey: sec.Key(), Position: sec.Position,
			Branch: sec.Branch, ParentBranch: sec.ParentBranch, BaseRef: sec.BaseRef, HeadRef: sec.HeadRef,
			Pending: sec.Pending, Patch: string(patch), Files: json.RawMessage(sec.FilesJSON),
			FileStates: fileStates, Organization: organization, Attributions: attributions,
		})
	}
	comments, err := s.st().ListCommentsByVersion(ctx, version.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wired := make([]wire.Comment, 0, len(comments))
	for _, c := range comments {
		replies, err := s.st().ListRepliesByComment(ctx, c.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		wired = append(wired, wire.ToComment(c, replies))
	}
	annotations, err := s.st().ListAnnotationsByVersion(ctx, version.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wiredAnnotations := make([]wire.Annotation, 0, len(annotations))
	for _, a := range annotations {
		wiredAnnotations = append(wiredAnnotations, wire.ToAnnotation(a, keyByID[a.SectionID]))
	}
	requests, err := s.st().ListAIRequests(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	aiRequests := make([]wire.AIRequest, 0, len(requests))
	for _, ar := range requests {
		aiRequests = append(aiRequests, wire.ToAIRequest(ar))
	}
	turnIDs := make([]int64, 0, len(turnIDSet))
	for tid := range turnIDSet {
		turnIDs = append(turnIDs, tid)
	}
	storeTurns, err := s.turnStore().ListTurnsByIDs(ctx, turnIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	turns := make([]wire.Turn, 0, len(storeTurns))
	for _, t := range storeTurns {
		turns = append(turns, wire.ToTurn(t))
	}
	latestSeq, err := s.st().MaxEventSeq(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		Review:          wire.ToReview(review, version.Branch),
		Version:         version.VersionNumber,
		VersionID:       strconv.FormatInt(version.ID, 10),
		Sections:        sectionResp,
		Comments:        wired,
		Annotations:     wiredAnnotations,
		AIRequests:      aiRequests,
		Turns:           turns,
		TurnActivity:    s.turnActivity(storeTurns),
		ClaudeConnected: s.connected(review.ID),
		LatestEventSeq:  strconv.FormatInt(latestSeq, 10),
	})
}

// turnActivity collects each turn's decision-ledger rows, keyed by the turn's
// wire id. The ledger is shared telemetry written concurrently by other
// cc-family tools, so a read failure degrades to an empty panel with a log
// line instead of failing the session.
func (s *Server) turnActivity(turns []vcs.Turn) map[string][]wire.Decision {
	out := make(map[string][]wire.Decision, len(turns))
	for _, t := range turns {
		untilMs := t.EndedAt
		if untilMs == 0 {
			untilMs = time.Now().UnixMilli()
		}
		rows, err := s.decisions.ForTurn(t.SessionID, t.StartedAt, untilMs)
		if err != nil {
			s.log.Printf("turn activity: decisions for turn %d: %v", t.ID, err)
			rows = nil
		}
		wired := make([]wire.Decision, 0, len(rows))
		for _, d := range rows {
			wired = append(wired, wire.ToDecision(d))
		}
		out[strconv.FormatInt(t.ID, 10)] = wired
	}
	return out
}

func (s *Server) handleGetVersions(w http.ResponseWriter, r *http.Request) {
	review, err := s.st().GetReviewByRef(r.Context(), r.PathValue("reviewId"))
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	versions, err := s.st().ListVersions(r.Context(), review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]wire.VersionSummary, 0, len(versions))
	for _, v := range versions {
		sections, err := s.st().ListSections(r.Context(), v.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = append(out, wire.ToVersionSummary(v, sections))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createCommentReq
	if !readJSON(w, r, &req) {
		return
	}
	sectionID, err := strconv.ParseInt(req.SectionID, 10, 64)
	if err != nil {
		http.Error(w, "bad sectionId", http.StatusBadRequest)
		return
	}
	section, err := s.st().GetSection(ctx, sectionID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	version, err := s.st().GetVersionByID(ctx, section.VersionID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	c := store.Comment{
		VersionID: section.VersionID, SectionID: section.ID, Branch: section.Key(), Pending: section.Pending,
		FilePath: req.FilePath, Side: req.Side,
		StartLine: req.Range.Start, EndLine: req.Range.End,
		StartSide: req.Range.StartSide, EndSide: req.Range.EndSide,
		LineContent: req.LineContent, Body: req.Body, Author: store.OriginUser, Status: "open",
	}
	// The currency check lives inside CreateComment's tx so a version minted
	// between here and the insert (httpapi never holds the daemon RepoLock)
	// can't strand the comment on a superseded version.
	id, err := s.st().CreateComment(ctx, c)
	if err != nil {
		if errors.Is(err, store.ErrStaleSection) {
			http.Error(w, "section belongs to a superseded version; reload the review", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.ID = id
	c.CreatedAt = time.Now()
	s.emit(ctx, version.ReviewID, ccevent.OriginHuman, store.EventCommentCreated, version.VersionNumber,
		map[string]any{"commentId": strconv.FormatInt(id, 10), "comment": wire.ToComment(c, nil)})
	writeJSON(w, http.StatusOK, map[string]string{"id": strconv.FormatInt(id, 10)})
}

func (s *Server) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req updateCommentReq
	if !readJSON(w, r, &req) {
		return
	}
	reviewID, versionNumber, err := s.st().ResolveCommentContext(ctx, id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if req.Body != nil {
		if err := s.st().UpdateCommentBody(ctx, id, *req.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	resolved := false
	if req.Status != "" {
		if err := s.st().UpdateCommentStatus(ctx, id, req.Status); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resolved = req.Status == "resolved"
	}
	if resolved {
		s.emit(ctx, reviewID, ccevent.OriginHuman, store.EventCommentResolved, versionNumber,
			map[string]any{"commentId": strconv.FormatInt(id, 10)})
	} else {
		s.emitComment(ctx, reviewID, store.EventCommentUpdated, versionNumber, id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCreateReply(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	commentID, _ := strconv.ParseInt(r.PathValue("commentId"), 10, 64)
	var req createReplyReq
	if !readJSON(w, r, &req) {
		return
	}
	reviewID, versionNumber, err := s.st().ResolveCommentContext(ctx, commentID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	// An ask answer mutates the ask reply itself — no sibling reply row, so the
	// thread has exactly one source of truth for the structured answer.
	if req.AskAnswer != nil {
		qrid, perr := strconv.ParseInt(req.QuestionReplyID, 10, 64)
		if perr != nil {
			http.Error(w, "askAnswer requires questionReplyId", http.StatusBadRequest)
			return
		}
		// The open check rides inside the UPDATE: an answer racing Submit gets
		// a 409, never a silent write into an already-frozen feedback file.
		if err := s.st().AnswerAskIfOpen(ctx, qrid, *req.AskAnswer, "web"); err != nil {
			switch {
			case errors.Is(err, store.ErrReviewNotOpen):
				http.Error(w, "review is submitted: Claude will ask this directly", http.StatusConflict)
			case errors.Is(err, store.ErrNotFound):
				http.Error(w, err.Error(), http.StatusNotFound)
			default:
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
			return
		}
		s.emitComment(ctx, reviewID, store.EventCommentUpdated, versionNumber, commentID)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	id, _, err := s.st().CreateReply(ctx, store.Reply{
		CommentID: commentID, Origin: store.OriginUser, Kind: "note", Body: req.Body,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// A user inline reply streams back as comment.updated carrying the refreshed
	// thread (the SPA has no separate user.reply event), and origin=user lets the
	// Claude-side stream see it.
	s.emitComment(ctx, reviewID, store.EventCommentUpdated, versionNumber, commentID)
	writeJSON(w, http.StatusOK, map[string]string{"id": strconv.FormatInt(id, 10)})
}

// handleSetFileStates applies the human's checkbox/hide clicks: the same
// validation as the daemon's file-states handler, emitted under origin user.
func (s *Server) handleSetFileStates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req fileStatesReq
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Files) == 0 {
		http.Error(w, "files required", http.StatusBadRequest)
		return
	}
	review, err := s.st().GetReviewByRef(ctx, req.ReviewID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	version, ok, err := s.st().LatestVersion(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	sections, err := s.st().ListSections(ctx, version.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fingerprints := make(map[store.SectionFileKey]string)
	for _, sec := range sections {
		files, err := sec.Files()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, f := range files {
			fingerprints[store.SectionFileKey{SectionKey: sec.Key(), Path: f.Path}] = f.Fingerprint
		}
	}
	var unknown []string
	inputs := make([]store.FileStateInput, 0, len(req.Files))
	for _, f := range req.Files {
		if _, ok := fingerprints[store.SectionFileKey{SectionKey: f.SectionKey, Path: f.Path}]; !ok {
			unknown = append(unknown, f.Path)
			continue
		}
		inputs = append(inputs, store.FileStateInput{SectionKey: f.SectionKey, Path: f.Path, Reviewed: f.Reviewed, Hidden: f.Hidden})
	}
	if len(unknown) > 0 {
		http.Error(w, "unknown paths: "+strings.Join(unknown, ", "), http.StatusBadRequest)
		return
	}
	results, err := s.st().ApplyFileStates(ctx, review.ID, inputs, fingerprints)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]fileStateOut, 0, len(results))
	states := make([]map[string]any, 0, len(results))
	for _, res := range results {
		out = append(out, fileStateOut{SectionKey: res.SectionKey, Path: res.Path, Reviewed: res.Applied.Reviewed, Hidden: res.Applied.Hidden})
		states = append(states, map[string]any{"sectionKey": res.SectionKey, "path": res.Path, "reviewed": res.Applied.Reviewed, "hidden": res.Applied.Hidden})
	}
	// Apply and emit are not atomic: racing with the daemon's file-states
	// handler, same-path events can land in the opposite order of the DB
	// applies. Accepted — the session GET is authoritative, so replay converges
	// on the next load.
	s.emit(ctx, review.ID, ccevent.OriginHuman, store.EventFileStates, version.VersionNumber,
		map[string]any{"states": states})
	writeJSON(w, http.StatusOK, map[string]any{"states": out})
}

// handleCreateAIRequest queues an AI-bar prompt for the live Claude session.
func (s *Server) handleCreateAIRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createAIRequestReq
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "prompt required", http.StatusBadRequest)
		return
	}
	review, err := s.st().GetReviewByRef(ctx, req.ReviewID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	version, ok, err := s.st().LatestVersion(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	ar, err := s.st().CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wired := wire.ToAIRequest(ar)
	s.emit(ctx, review.ID, ccevent.OriginHuman, store.EventAIRequestCreated, ar.VersionNumber,
		map[string]any{"request": wired})
	writeJSON(w, http.StatusOK, map[string]any{
		"request": wired, "claudeConnected": s.connected(review.ID),
	})
}

// handleAnswerAIRequest records the reviewer's answer to a parked clarifying
// question (awaiting_input→answered) and bumps attempt, so the daemon redelivers
// the request to a fresh organize agent carrying the original prompt plus the
// question and answer. A version-stale or non-awaiting request is 409.
func (s *Server) handleAnswerAIRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad ai request id", http.StatusBadRequest)
		return
	}
	var req answerAIRequestReq
	if !readJSON(w, r, &req) {
		return
	}
	ar, err := s.st().GetAIRequest(ctx, id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if ar.Status != "awaiting_input" {
		http.Error(w, fmt.Sprintf("ai request %d is %q, only an awaiting_input request can be answered", id, ar.Status), http.StatusConflict)
		return
	}
	version, ok, err := s.st().LatestVersion(ctx, ar.ReviewID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	if ar.VersionNumber != version.VersionNumber {
		http.Error(w, "the diff changed since this question was asked — re-ask in the AI bar", http.StatusConflict)
		return
	}
	var answer store.AIAnswer
	if ar.Question != nil && ar.Question.Ask != nil {
		if req.AskAnswer == nil {
			http.Error(w, "this question needs a structured answer", http.StatusBadRequest)
			return
		}
		if err := ar.Question.Ask.ValidateAnswer(*req.AskAnswer); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answer.AskAnswer = req.AskAnswer
	} else if strings.TrimSpace(req.Answer) == "" {
		http.Error(w, "answer required", http.StatusBadRequest)
		return
	} else {
		answer.Text = req.Answer
	}
	updated, err := s.st().AnswerAIRequest(ctx, id, answer)
	if err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// EventAIRequestCreated (not updated) routes the answered request onto the
	// skill's dispatch path so a fresh organize agent picks it up.
	s.emit(ctx, ar.ReviewID, ccevent.OriginHuman, store.EventAIRequestCreated, version.VersionNumber,
		map[string]any{"request": wire.ToAIRequest(updated)})
	writeJSON(w, http.StatusOK, map[string]any{
		"request": wire.ToAIRequest(updated), "claudeConnected": s.connected(ar.ReviewID),
	})
}

// handleUndoAIRequest reverts a done request's batch: the recorded priors are
// restored first (winning over any later human changes), then the guarded
// done→undone transition (409 otherwise) commits the undo. A failed restore
// leaves the request done, so Undo stays retryable.
func (s *Server) handleUndoAIRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad ai request id", http.StatusBadRequest)
		return
	}
	ar, err := s.st().GetAIRequest(ctx, id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	// Both emissions below carry the review's current version, not the version
	// the request was created on — a later version may be the one on screen.
	version, ok, err := s.st().LatestVersion(ctx, ar.ReviewID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	// The status pre-check keeps the 409 path free of state writes (restoring a
	// working request's partial changes would corrupt its in-flight batch); the
	// transition guard below still wins any race.
	if ar.Status != "done" {
		http.Error(w, fmt.Sprintf("ai request %d is %q, only done requests can be undone", id, ar.Status), http.StatusConflict)
		return
	}
	if err := s.st().RestoreFileStates(ctx, ar.ReviewID, ar.Changes); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := s.st().TransitionAIRequest(ctx, id, "undone", "", nil)
	if err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(ar.Changes) > 0 {
		states := make([]map[string]any, 0, len(ar.Changes))
		for _, c := range ar.Changes {
			states = append(states, map[string]any{"sectionKey": c.SectionKey, "path": c.Path, "reviewed": c.Prior.Reviewed, "hidden": c.Prior.Hidden})
		}
		s.emit(ctx, ar.ReviewID, ccevent.OriginHuman, store.EventFileStates, version.VersionNumber,
			map[string]any{"states": states, "undoOf": strconv.FormatInt(id, 10)})
	}
	// Undo also clears any highlights the request added (comment-kind annotations
	// are real threads and outlive undo, managed via the comment UI).
	if deleted, err := s.st().DeleteAnnotationsByAIRequest(ctx, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if deleted > 0 {
		list, err := s.st().ListAnnotationsByVersion(ctx, version.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sections, err := s.st().ListSections(ctx, version.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		keyByID := make(map[int64]string, len(sections))
		for _, sec := range sections {
			keyByID[sec.ID] = sec.Key()
		}
		wiredAnnotations := make([]wire.Annotation, 0, len(list))
		for _, a := range list {
			wiredAnnotations = append(wiredAnnotations, wire.ToAnnotation(a, keyByID[a.SectionID]))
		}
		s.emit(ctx, ar.ReviewID, ccevent.OriginHuman, store.EventAnnotationsUpdated, version.VersionNumber,
			map[string]any{"annotations": wiredAnnotations})
	}
	s.emit(ctx, ar.ReviewID, ccevent.OriginHuman, store.EventAIRequestUpdated, version.VersionNumber,
		map[string]any{"request": wire.ToAIRequest(updated)})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req submitReq
	if !readJSON(w, r, &req) {
		return
	}
	review, err := s.st().GetReviewByRef(ctx, req.ReviewID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	version, ok, err := s.st().LatestVersion(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	submittedAt := time.Now()
	fb, err := feedback.Build(ctx, s.st(), review.ID, version, submittedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := paths.EnsureReviewDir(review.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fbPath := paths.FeedbackPath(review.ID, version.VersionNumber)
	if err := feedback.Freeze(fbPath, fb); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The corrections ledger is an additional, best-effort write alongside the
	// frozen snapshot: a failed shell-out to cc-transcript must not strand a
	// submitted review whose feedback.json is already on disk.
	if err := corrections.Write(ctx, fb, review.RepoRoot, submittedAt); err != nil {
		s.log.Printf("corrections: write for review %s failed: %v", review.ID, err)
	}
	if err := s.subjectStore().SetStatus(ctx, review.ID, "submitted"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.emit(ctx, review.ID, ccevent.OriginSystem, store.EventSubmit, version.VersionNumber,
		map[string]any{"feedbackPath": fbPath})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "feedbackPath": fbPath})
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req closeReq
	if !readJSON(w, r, &req) {
		return
	}
	review, err := s.st().GetReviewByRef(ctx, req.ReviewID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	swapped, err := s.st().CloseAndDetach(ctx, s.subjectStore(), review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !swapped {
		http.Error(w, fmt.Sprintf("review is %s; only an open or expired review can be closed", review.Status),
			http.StatusConflict)
		return
	}
	if version, ok, err := s.st().LatestVersion(ctx, review.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if ok {
		s.emit(ctx, review.ID, ccevent.OriginHuman, store.EventStatusChanged, version.VersionNumber,
			map[string]any{"status": "closed"})
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- helpers ---------------------------------------------------------------

func (s *Server) emit(ctx context.Context, reviewID, origin, typ string, version int, fields map[string]any) {
	_, _ = s.append(ctx, &ccevent.Event{
		SubjectID: reviewID, Origin: origin, Type: typ, Payload: wire.Event(typ, version, fields),
	})
}

// emitComment re-reads a comment with its replies and emits it under typ.
func (s *Server) emitComment(ctx context.Context, reviewID, typ string, version int, commentID int64) {
	c, err := s.st().GetComment(ctx, commentID)
	if err != nil {
		return
	}
	replies, err := s.st().ListRepliesByComment(ctx, commentID)
	if err != nil {
		return
	}
	s.emit(ctx, reviewID, ccevent.OriginHuman, typ, version,
		map[string]any{"commentId": strconv.FormatInt(commentID, 10), "comment": wire.ToComment(c, replies)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func notFoundOr500(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, vcs.ErrTurnNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
