package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/yasyf/cc-review/internal/feedback"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

// --- wire types ------------------------------------------------------------

type sessionResponse struct {
	Review    wire.Review     `json:"review"`
	Version   int             `json:"version"`
	VersionID string          `json:"versionId"`
	Files     json.RawMessage `json:"files"`
	Patch     string          `json:"patchText"`
	Comments  []wire.Comment  `json:"comments"`
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
	Answer          string `json:"answer"`
	Body            string `json:"body"`
	QuestionReplyID string `json:"questionReplyId"`
}

type submitReq struct {
	ReviewID string `json:"reviewId"`
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
	writeJSON(w, http.StatusOK, sessionResponse{
		Review:    wire.ToReview(review, version.Branch),
		Version:   version.VersionNumber,
		VersionID: strconv.FormatInt(version.ID, 10),
		Files:     json.RawMessage(version.FilesJSON),
		Patch:     string(patch),
		Comments:  wired,
	})
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
	s.emit(ctx, version.ReviewID, store.OriginUser, store.EventCommentCreated, version.VersionNumber,
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
		s.emit(ctx, reviewID, store.OriginUser, store.EventCommentResolved, versionNumber,
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
	body := req.Body
	kind := "note"
	if req.Answer != "" {
		body = req.Answer
		kind = "answer"
	}
	id, _, err := s.store.CreateReply(ctx, store.Reply{
		CommentID: commentID, Origin: store.OriginUser, Kind: kind, Body: body,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if req.QuestionReplyID != "" {
		if qrid, perr := strconv.ParseInt(req.QuestionReplyID, 10, 64); perr == nil {
			if err := s.store.AnswerReply(ctx, qrid, body, "web"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	// A user inline reply streams back as comment.updated carrying the refreshed
	// thread (the SPA has no separate user.reply event), and origin=user lets the
	// Claude-side stream see it.
	s.emitComment(ctx, reviewID, store.EventCommentUpdated, versionNumber, commentID)
	writeJSON(w, http.StatusOK, map[string]string{"id": strconv.FormatInt(id, 10)})
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
	if err := s.store.SetReviewStatus(ctx, review.ID, "submitted"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.emit(ctx, review.ID, store.OriginSystem, store.EventSubmit, version.VersionNumber,
		map[string]any{"feedbackPath": fbPath})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "feedbackPath": fbPath})
}

// --- helpers ---------------------------------------------------------------

func (s *Server) emit(ctx context.Context, reviewID, origin, typ string, version int, fields map[string]any) {
	_, _ = s.backend.AppendEvent(ctx, &store.Event{
		ReviewID: reviewID, Origin: origin, Type: typ, VersionNumber: version, Payload: wire.Event(typ, version, fields),
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
	s.emit(ctx, reviewID, store.OriginUser, typ, version,
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
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
