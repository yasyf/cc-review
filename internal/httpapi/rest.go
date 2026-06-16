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

	"github.com/yasyf/cc-review/internal/feedback"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

// --- wire types ------------------------------------------------------------

type sessionResponse struct {
	Review          wire.Review                        `json:"review"`
	Version         int                                `json:"version"`
	VersionID       string                             `json:"versionId"`
	Files           json.RawMessage                    `json:"files"`
	Patch           string                             `json:"patchText"`
	Comments        []wire.Comment                     `json:"comments"`
	FileStates      map[string]wire.FileState          `json:"fileStates"`
	Organization    *store.Organization                `json:"organization"`
	AIRequests      []wire.AIRequest                   `json:"aiRequests"`
	Turns           []wire.Turn                        `json:"turns"`
	Attributions    map[string][]wire.AttributionRange `json:"attributions"`
	TurnActivity    map[string][]wire.Decision         `json:"turnActivity"`
	ClaudeConnected bool                               `json:"claudeConnected"`
	LatestEventSeq  string                             `json:"latestEventSeq"`
}

type createCommentReq struct {
	VersionID string `json:"versionId"`
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

type fileStatesReq struct {
	ReviewID string `json:"reviewId"`
	Files    []struct {
		Path     string `json:"path"`
		Reviewed *bool  `json:"reviewed"`
		Hidden   *bool  `json:"hidden"`
	} `json:"files"`
}

type fileStateOut struct {
	Path     string `json:"path"`
	Reviewed bool   `json:"reviewed"`
	Hidden   bool   `json:"hidden"`
}

type createAIRequestReq struct {
	ReviewID string `json:"reviewId"`
	Prompt   string `json:"prompt"`
}

// --- handlers --------------------------------------------------------------

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	review, err := s.store.GetReviewByRef(ctx, r.PathValue("reviewId"))
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	var version store.Version
	if v := r.URL.Query().Get("version"); v != "" {
		n, _ := strconv.Atoi(v)
		version, err = s.store.GetVersion(ctx, review.ID, n)
	} else {
		var ok bool
		version, ok, err = s.store.LatestVersion(ctx, review.ID)
		if err == nil && !ok {
			err = store.ErrNotFound
		}
	}
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	patch, err := os.ReadFile(version.PatchPath)
	if err != nil {
		http.Error(w, "read patch: "+err.Error(), http.StatusInternalServerError)
		return
	}
	comments, err := s.store.ListCommentsByVersion(ctx, version.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wired := make([]wire.Comment, 0, len(comments))
	for _, c := range comments {
		replies, err := s.store.ListRepliesByComment(ctx, c.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		wired = append(wired, wire.ToComment(c, replies))
	}
	files, err := version.Files()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	inVersion := make(map[string]bool, len(files))
	for _, f := range files {
		inVersion[f.Path] = true
	}
	states, err := s.store.ListFileStates(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fileStates := make(map[string]wire.FileState, len(states))
	for _, st := range states {
		if inVersion[st.Path] {
			fileStates[st.Path] = wire.FileState{Reviewed: st.Reviewed, Hidden: st.Hidden}
		}
	}
	var organization *store.Organization
	if org, ok, err := s.store.GetOrganization(ctx, version.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if ok {
		organization = &org
	}
	requests, err := s.store.ListAIRequests(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	aiRequests := make([]wire.AIRequest, 0, len(requests))
	for _, ar := range requests {
		aiRequests = append(aiRequests, wire.ToAIRequest(ar))
	}
	attrsByFile, err := s.store.ListAttributionsByVersion(ctx, version.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	attributions := make(map[string][]wire.AttributionRange, len(attrsByFile))
	turnIDSet := make(map[int64]bool)
	for path, ranges := range attrsByFile {
		out := make([]wire.AttributionRange, 0, len(ranges))
		for _, rg := range ranges {
			out = append(out, wire.ToAttributionRange(rg))
			if rg.TurnID != 0 {
				turnIDSet[rg.TurnID] = true
			}
		}
		attributions[path] = out
	}
	turnIDs := make([]int64, 0, len(turnIDSet))
	for tid := range turnIDSet {
		turnIDs = append(turnIDs, tid)
	}
	storeTurns, err := s.turns.ListTurnsByIDs(ctx, turnIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	turns := make([]wire.Turn, 0, len(storeTurns))
	for _, t := range storeTurns {
		turns = append(turns, wire.ToTurn(t))
	}
	latestSeq, err := s.store.MaxEventSeq(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		Review:          wire.ToReview(review, version.Branch),
		Version:         version.VersionNumber,
		VersionID:       strconv.FormatInt(version.ID, 10),
		Files:           json.RawMessage(version.FilesJSON),
		Patch:           string(patch),
		Comments:        wired,
		FileStates:      fileStates,
		Organization:    organization,
		AIRequests:      aiRequests,
		Turns:           turns,
		Attributions:    attributions,
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
	review, err := s.store.GetReviewByRef(r.Context(), r.PathValue("reviewId"))
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	versions, err := s.store.ListVersions(r.Context(), review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]wire.VersionSummary, 0, len(versions))
	for _, v := range versions {
		out = append(out, wire.ToVersionSummary(v))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createCommentReq
	if !readJSON(w, r, &req) {
		return
	}
	versionID, err := strconv.ParseInt(req.VersionID, 10, 64)
	if err != nil {
		http.Error(w, "bad versionId", http.StatusBadRequest)
		return
	}
	version, err := s.store.GetVersionByID(ctx, versionID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	c := store.Comment{
		VersionID: versionID, FilePath: req.FilePath, Side: req.Side,
		StartLine: req.Range.Start, EndLine: req.Range.End,
		StartSide: req.Range.StartSide, EndSide: req.Range.EndSide,
		LineContent: req.LineContent, Body: req.Body, Author: store.OriginUser, Status: "open",
	}
	id, err := s.store.CreateComment(ctx, c)
	if err != nil {
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
	reviewID, versionNumber, err := s.store.ResolveCommentContext(ctx, id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if req.Body != nil {
		if err := s.store.UpdateCommentBody(ctx, id, *req.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	resolved := false
	if req.Status != "" {
		if err := s.store.UpdateCommentStatus(ctx, id, req.Status); err != nil {
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
	reviewID, versionNumber, err := s.store.ResolveCommentContext(ctx, commentID)
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
		if err := s.store.AnswerAskIfOpen(ctx, qrid, *req.AskAnswer, "web"); err != nil {
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
	id, _, err := s.store.CreateReply(ctx, store.Reply{
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
	review, err := s.store.GetReviewByRef(ctx, req.ReviewID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	version, ok, err := s.store.LatestVersion(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	files, err := version.Files()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fingerprints := make(map[string]string, len(files))
	for _, f := range files {
		fingerprints[f.Path] = f.Fingerprint
	}
	var unknown []string
	inputs := make([]store.FileStateInput, 0, len(req.Files))
	for _, f := range req.Files {
		if _, ok := fingerprints[f.Path]; !ok {
			unknown = append(unknown, f.Path)
			continue
		}
		inputs = append(inputs, store.FileStateInput{Path: f.Path, Reviewed: f.Reviewed, Hidden: f.Hidden})
	}
	if len(unknown) > 0 {
		http.Error(w, "unknown paths: "+strings.Join(unknown, ", "), http.StatusBadRequest)
		return
	}
	results, err := s.store.ApplyFileStates(ctx, review.ID, inputs, fingerprints)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]fileStateOut, 0, len(results))
	states := make([]map[string]any, 0, len(results))
	for _, res := range results {
		out = append(out, fileStateOut{Path: res.Path, Reviewed: res.Applied.Reviewed, Hidden: res.Applied.Hidden})
		states = append(states, map[string]any{"path": res.Path, "reviewed": res.Applied.Reviewed, "hidden": res.Applied.Hidden})
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
	review, err := s.store.GetReviewByRef(ctx, req.ReviewID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	version, ok, err := s.store.LatestVersion(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	ar, err := s.store.CreateAIRequest(ctx, review.ID, version.VersionNumber, store.OriginUser, req.Prompt)
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
	ar, err := s.store.GetAIRequest(ctx, id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	// Both emissions below carry the review's current version, not the version
	// the request was created on — a later version may be the one on screen.
	version, ok, err := s.store.LatestVersion(ctx, ar.ReviewID)
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
	if err := s.store.RestoreFileStates(ctx, ar.ReviewID, ar.Changes); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := s.store.TransitionAIRequest(ctx, id, "undone", "", nil)
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
			states = append(states, map[string]any{"path": c.Path, "reviewed": c.Prior.Reviewed, "hidden": c.Prior.Hidden})
		}
		s.emit(ctx, ar.ReviewID, ccevent.OriginHuman, store.EventFileStates, version.VersionNumber,
			map[string]any{"states": states, "undoOf": strconv.FormatInt(id, 10)})
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
	review, err := s.store.GetReviewByRef(ctx, req.ReviewID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	version, ok, err := s.store.LatestVersion(ctx, review.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "review has no versions", http.StatusBadRequest)
		return
	}
	fb, err := feedback.Build(ctx, s.store, review.ID, version, time.Now())
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
	if err := s.subjects.SetStatus(ctx, review.ID, "submitted"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.emit(ctx, review.ID, ccevent.OriginSystem, store.EventSubmit, version.VersionNumber,
		map[string]any{"feedbackPath": fbPath})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "feedbackPath": fbPath})
}

// --- helpers ---------------------------------------------------------------

func (s *Server) emit(ctx context.Context, reviewID, origin, typ string, version int, fields map[string]any) {
	_, _ = s.append(ctx, &ccevent.Event{
		SubjectID: reviewID, Origin: origin, Type: typ, Payload: wire.Event(typ, version, fields),
	})
}

// emitComment re-reads a comment with its replies and emits it under typ.
func (s *Server) emitComment(ctx context.Context, reviewID, typ string, version int, commentID int64) {
	c, err := s.store.GetComment(ctx, commentID)
	if err != nil {
		return
	}
	replies, err := s.store.ListRepliesByComment(ctx, commentID)
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
