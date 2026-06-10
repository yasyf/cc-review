package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

// organizePrompt seeds the system AI request handleStart creates per version,
// asking the live Claude session to chapter the diff.
const organizePrompt = "Organize this review into chapters and rate per-file risk."

// reviewWithLatest resolves the window's review and its latest version; a
// non-nil Response is the failure to return as-is.
func (s *Server) reviewWithLatest(ctx context.Context, req Request) (store.Review, store.Version, *Response) {
	review, ok, err := s.lookupReview(ctx, req)
	if err != nil {
		r := errResp(err.Error())
		return store.Review{}, store.Version{}, &r
	}
	if !ok {
		r := errResp("no review for this session/repo")
		return store.Review{}, store.Version{}, &r
	}
	v, ok, err := s.store.LatestVersion(ctx, review.ID)
	if err != nil {
		r := errResp(err.Error())
		return store.Review{}, store.Version{}, &r
	}
	if !ok {
		r := errResp("review has no versions")
		return store.Review{}, store.Version{}, &r
	}
	return review, v, nil
}

func (s *Server) handleFileStates(ctx context.Context, req Request) Response {
	if len(req.Files) == 0 {
		return errResp("file-states requires at least one file")
	}
	review, v, fail := s.reviewWithLatest(ctx, req)
	if fail != nil {
		return *fail
	}
	fingerprints, err := versionFingerprints(v)
	if err != nil {
		return errResp(err.Error())
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
		return errResp(fmt.Sprintf("unknown paths (not in version %d): %s", v.VersionNumber, strings.Join(unknown, ", ")))
	}
	if req.AIRequestID != 0 {
		ar, err := s.store.GetAIRequest(ctx, req.AIRequestID)
		if err != nil {
			return errResp(err.Error())
		}
		if ar.ReviewID != review.ID {
			return errResp(fmt.Sprintf("ai request %d does not belong to this review", req.AIRequestID))
		}
		if ar.Status != "pending" && ar.Status != "working" {
			return errResp(fmt.Sprintf("ai request %d is %q: changes are only recorded while pending or working", req.AIRequestID, ar.Status))
		}
	}
	results, err := s.store.ApplyFileStates(ctx, review.ID, inputs, fingerprints)
	if err != nil {
		return errResp(err.Error())
	}
	if req.AIRequestID != 0 {
		changes := make([]store.AIChange, 0, len(results))
		for i, res := range results {
			changes = append(changes, store.AIChange{
				Path: res.Path, Reason: req.Files[i].Reason, Prior: res.Prior, Applied: res.Applied,
			})
		}
		if err := s.store.AppendAIRequestChanges(ctx, req.AIRequestID, changes); err != nil {
			return errResp(err.Error())
		}
	}
	states := make([]map[string]any, 0, len(results))
	for i, res := range results {
		st := map[string]any{"path": res.Path, "reviewed": res.Applied.Reviewed, "hidden": res.Applied.Hidden}
		if reason := req.Files[i].Reason; reason != "" {
			st["reason"] = reason
		}
		states = append(states, st)
	}
	fields := map[string]any{"states": states}
	if req.AIRequestID != 0 {
		fields["aiRequestId"] = strconv.FormatInt(req.AIRequestID, 10)
	}
	// Apply and emit are not atomic: racing with the REST file-states handler,
	// same-path events can land in the opposite order of the DB applies.
	// Accepted — the session GET is authoritative, so replay converges on the
	// next load.
	_, _ = s.AppendEvent(ctx, &store.Event{
		ReviewID: review.ID, Origin: store.OriginClaude, Type: store.EventFileStates, VersionNumber: v.VersionNumber,
		Payload: wire.Event(store.EventFileStates, v.VersionNumber, fields),
	})
	return Response{OK: true}
}

func (s *Server) handleUpdateAIRequest(ctx context.Context, req Request) Response {
	switch req.AIStatus {
	case "working", "done", "failed":
	default:
		return errResp(fmt.Sprintf("update-ai-request status %q: want working | done | failed", req.AIStatus))
	}
	review, v, fail := s.reviewWithLatest(ctx, req)
	if fail != nil {
		return *fail
	}
	ar, err := s.store.GetAIRequest(ctx, req.AIRequestID)
	if err != nil {
		return errResp(err.Error())
	}
	if ar.ReviewID != review.ID {
		return errResp(fmt.Sprintf("ai request %d does not belong to this review", req.AIRequestID))
	}
	updated, err := s.store.TransitionAIRequest(ctx, req.AIRequestID, req.AIStatus, req.Summary, req.Unmatched)
	if err != nil {
		return errResp(err.Error())
	}
	s.emitAIRequest(ctx, store.OriginClaude, store.EventAIRequestUpdated, v.VersionNumber, updated)
	return Response{OK: true}
}

func (s *Server) handleSubmitOrganization(ctx context.Context, req Request) Response {
	if req.Organization == nil {
		return errResp("submit-organization requires chapters")
	}
	review, v, fail := s.reviewWithLatest(ctx, req)
	if fail != nil {
		return *fail
	}
	if req.VersionNumber != 0 && req.VersionNumber != v.VersionNumber {
		return errResp(fmt.Sprintf(
			"stale version_number %d: the current version is %d — re-run get_review_files against the latest diff and resubmit",
			req.VersionNumber, v.VersionNumber))
	}
	files, err := v.Files()
	if err != nil {
		return errResp(err.Error())
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if err := req.Organization.Validate(paths); err != nil {
		return errResp(err.Error())
	}
	if err := s.store.UpsertOrganization(ctx, v.ID, *req.Organization); err != nil {
		return errResp(err.Error())
	}
	_, _ = s.AppendEvent(ctx, &store.Event{
		ReviewID: review.ID, Origin: store.OriginClaude, Type: store.EventOrganizationUpdated, VersionNumber: v.VersionNumber,
		Payload: wire.Event(store.EventOrganizationUpdated, v.VersionNumber, map[string]any{"organization": *req.Organization}),
	})
	return Response{OK: true}
}

func (s *Server) handleReviewFiles(ctx context.Context, req Request) Response {
	review, v, fail := s.reviewWithLatest(ctx, req)
	if fail != nil {
		return *fail
	}
	files, err := v.Files()
	if err != nil {
		return errResp(err.Error())
	}
	states, err := s.store.ListFileStates(ctx, review.ID)
	if err != nil {
		return errResp(err.Error())
	}
	byPath := make(map[string]store.FileState, len(states))
	for _, st := range states {
		byPath[st.Path] = st
	}
	entries := make([]map[string]any, 0, len(files))
	for _, f := range files {
		e := map[string]any{
			"path": f.Path, "status": f.Status,
			"reviewed": byPath[f.Path].Reviewed, "hidden": byPath[f.Path].Hidden,
		}
		if f.OldPath != "" {
			e["old_path"] = f.OldPath
		}
		entries = append(entries, e)
	}
	b, err := json.Marshal(map[string]any{"version_number": v.VersionNumber, "files": entries})
	if err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true, ReviewFiles: b}
}

// emitAIRequest appends an ai.request.* event carrying the full request
// object, stamped with the review's CURRENT version — not ar.VersionNumber,
// which is frozen at creation and goes stale once a later version lands.
func (s *Server) emitAIRequest(ctx context.Context, origin, typ string, versionNumber int, ar store.AIRequest) {
	_, _ = s.AppendEvent(ctx, &store.Event{
		ReviewID: ar.ReviewID, Origin: origin, Type: typ, VersionNumber: versionNumber,
		Payload: wire.Event(typ, versionNumber, map[string]any{"request": wire.ToAIRequest(ar)}),
	})
}

// versionFingerprints maps a version's paths to their diff fingerprints.
func versionFingerprints(v store.Version) (map[string]string, error) {
	files, err := v.Files()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = f.Fingerprint
	}
	return out, nil
}
