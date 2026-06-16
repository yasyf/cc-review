package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	ccd "github.com/yasyf/cc-interact/daemon"
	ccevent "github.com/yasyf/cc-interact/event"
	"github.com/yasyf/cc-interact/subject"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/wire"
)

// reviewWithLatest resolves the window's review and its latest version; a
// non-nil Reply is the failure to return as-is.
func reviewWithLatest(hc ccd.HandlerCtx, st *store.Store) (subject.Subject, store.Version, *ccd.Reply) {
	sub, ok, err := hc.Subjects.Find(hc.Ctx, hc.Window, hc.Scope)
	if err != nil {
		r := errReply(err.Error())
		return subject.Subject{}, store.Version{}, &r
	}
	if !ok {
		r := errReply("no review for this session/repo")
		return subject.Subject{}, store.Version{}, &r
	}
	v, ok, err := st.LatestVersion(hc.Ctx, sub.ID)
	if err != nil {
		r := errReply(err.Error())
		return subject.Subject{}, store.Version{}, &r
	}
	if !ok {
		r := errReply("review has no versions")
		return subject.Subject{}, store.Version{}, &r
	}
	return sub, v, nil
}

func (rv *review) handleFileStates(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b := decodeBody(hc.Env.Body)
	if len(b.Files) == 0 {
		return errReply("file-states requires at least one file")
	}
	sub, v, fail := reviewWithLatest(hc, st)
	if fail != nil {
		return *fail
	}
	fingerprints, err := versionFingerprints(v)
	if err != nil {
		return errReply(err.Error())
	}
	var unknown []string
	inputs := make([]store.FileStateInput, 0, len(b.Files))
	for _, f := range b.Files {
		if _, ok := fingerprints[f.Path]; !ok {
			unknown = append(unknown, f.Path)
			continue
		}
		inputs = append(inputs, store.FileStateInput{Path: f.Path, Reviewed: f.Reviewed, Hidden: f.Hidden})
	}
	if len(unknown) > 0 {
		return errReply(fmt.Sprintf("unknown paths (not in version %d): %s", v.VersionNumber, strings.Join(unknown, ", ")))
	}
	if b.AIRequestID != 0 {
		ar, err := st.GetAIRequest(hc.Ctx, b.AIRequestID)
		if err != nil {
			return errReply(err.Error())
		}
		if ar.ReviewID != sub.ID {
			return errReply(fmt.Sprintf("ai request %d does not belong to this review", b.AIRequestID))
		}
		if ar.Status != "pending" && ar.Status != "working" {
			return errReply(fmt.Sprintf("ai request %d is %q: changes are only recorded while pending or working", b.AIRequestID, ar.Status))
		}
	}
	results, err := st.ApplyFileStates(hc.Ctx, sub.ID, inputs, fingerprints)
	if err != nil {
		return errReply(err.Error())
	}
	if b.AIRequestID != 0 {
		changes := make([]store.AIChange, 0, len(results))
		for i, res := range results {
			changes = append(changes, store.AIChange{
				Path: res.Path, Reason: b.Files[i].Reason, Prior: res.Prior, Applied: res.Applied,
			})
		}
		if err := st.AppendAIRequestChanges(hc.Ctx, b.AIRequestID, changes); err != nil {
			return errReply(err.Error())
		}
	}
	states := make([]map[string]any, 0, len(results))
	for i, res := range results {
		s := map[string]any{"path": res.Path, "reviewed": res.Applied.Reviewed, "hidden": res.Applied.Hidden}
		if reason := b.Files[i].Reason; reason != "" {
			s["reason"] = reason
		}
		states = append(states, s)
	}
	fields := map[string]any{"states": states}
	if b.AIRequestID != 0 {
		fields["aiRequestId"] = strconv.FormatInt(b.AIRequestID, 10)
	}
	// Apply and emit are not atomic: racing with the REST file-states handler,
	// same-path events can land in the opposite order of the DB applies. Accepted
	// — the session GET is authoritative, so replay converges on the next load.
	emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginAgent, store.EventFileStates, v.VersionNumber, fields)
	return ccd.Reply{OK: true}
}

func (rv *review) handleUpdateAIRequest(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b := decodeBody(hc.Env.Body)
	switch b.AIStatus {
	case "working", "done", "failed":
	default:
		return errReply(fmt.Sprintf("update-ai-request status %q: want working | done | failed", b.AIStatus))
	}
	sub, v, fail := reviewWithLatest(hc, st)
	if fail != nil {
		return *fail
	}
	ar, err := st.GetAIRequest(hc.Ctx, b.AIRequestID)
	if err != nil {
		return errReply(err.Error())
	}
	if ar.ReviewID != sub.ID {
		return errReply(fmt.Sprintf("ai request %d does not belong to this review", b.AIRequestID))
	}
	updated, err := st.TransitionAIRequest(hc.Ctx, b.AIRequestID, b.AIStatus, b.Summary, b.Unmatched)
	if err != nil {
		return errReply(err.Error())
	}
	emitAIRequest(hc.Ctx, hc.Append, ccevent.OriginAgent, store.EventAIRequestUpdated, v.VersionNumber, updated)
	return ccd.Reply{OK: true}
}

func (rv *review) handleSubmitOrganization(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b := decodeBody(hc.Env.Body)
	if b.Organization == nil {
		return errReply("submit-organization requires chapters")
	}
	sub, v, fail := reviewWithLatest(hc, st)
	if fail != nil {
		return *fail
	}
	if b.VersionNumber == 0 {
		return errReply("submit-organization requires version_number — take it from get_review_files")
	}
	if b.VersionNumber != v.VersionNumber {
		return errReply(fmt.Sprintf(
			"stale version_number %d: the current version is %d — re-run get_review_files against the latest diff and resubmit",
			b.VersionNumber, v.VersionNumber))
	}
	files, err := v.Files()
	if err != nil {
		return errReply(err.Error())
	}
	filePaths := make([]string, 0, len(files))
	for _, f := range files {
		filePaths = append(filePaths, f.Path)
	}
	if err := b.Organization.Validate(filePaths); err != nil {
		return errReply(err.Error())
	}
	if err := st.UpsertOrganization(hc.Ctx, v.ID, *b.Organization); err != nil {
		return errReply(err.Error())
	}
	emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginAgent, store.EventOrganizationUpdated, v.VersionNumber,
		map[string]any{"organization": *b.Organization})
	return ccd.Reply{OK: true}
}

func (rv *review) handleReviewFiles(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	sub, v, fail := reviewWithLatest(hc, st)
	if fail != nil {
		return *fail
	}
	files, err := v.Files()
	if err != nil {
		return errReply(err.Error())
	}
	states, err := st.ListFileStates(hc.Ctx, sub.ID)
	if err != nil {
		return errReply(err.Error())
	}
	byPath := make(map[string]store.FileState, len(states))
	for _, s := range states {
		byPath[s.Path] = s
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
	out := map[string]any{"version_number": v.VersionNumber, "patch_path": v.PatchPath, "files": entries}
	if org, basis, ok, err := st.LatestOrganization(hc.Ctx, sub.ID); err != nil {
		return errReply(err.Error())
	} else if ok {
		block, err := organizationContext(org, basis, v)
		if err != nil {
			return errReply(err.Error())
		}
		out["organization"] = block
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return errReply(err.Error())
	}
	return okReply(result{ReviewFiles: raw})
}

// organizationContext annotates the latest organization against the current
// version so the organize agent rebuilds incrementally instead of from scratch:
// per file a delta mark (absent = unchanged diff), plus the current paths the
// basis never covered. Files are joined by path first, then by base origin —
// every version diffs the review's pinned base, so a rename's old_path keys it
// back to the basis entry. A direct path match always wins over an origin join.
func organizationContext(org store.Organization, basis, current store.Version) (map[string]any, error) {
	basisFiles, err := basis.Files()
	if err != nil {
		return nil, err
	}
	currentFiles, err := current.Files()
	if err != nil {
		return nil, err
	}
	basisByPath := make(map[string]vcs.FileChange, len(basisFiles))
	for _, f := range basisFiles {
		basisByPath[f.Path] = f
	}
	currentByPath := make(map[string]vcs.FileChange, len(currentFiles))
	renamedByOrigin := make(map[string]vcs.FileChange)
	for _, f := range currentFiles {
		currentByPath[f.Path] = f
		if f.OldPath != "" {
			renamedByOrigin[f.OldPath] = f
		}
	}
	directClaimed := make(map[string]bool)
	for _, ch := range org.Chapters {
		for _, f := range ch.Files {
			if _, ok := currentByPath[f.Path]; ok {
				directClaimed[f.Path] = true
			}
		}
	}
	matched := make(map[string]bool, len(currentFiles))
	chapters := make([]map[string]any, 0, len(org.Chapters))
	for _, ch := range org.Chapters {
		files := make([]map[string]any, 0, len(ch.Files))
		for _, f := range ch.Files {
			entry := map[string]any{"path": f.Path, "risk": f.Risk, "rationale": f.Rationale}
			bf := basisByPath[f.Path]
			origin := bf.OldPath
			if origin == "" {
				origin = bf.Path
			}
			if cf, ok := currentByPath[f.Path]; ok {
				matched[f.Path] = true
				if cf.Fingerprint != bf.Fingerprint {
					entry["delta"] = "changed"
				}
			} else if cf, ok := renamedByOrigin[origin]; ok && !directClaimed[cf.Path] {
				matched[cf.Path] = true
				entry["delta"] = "moved"
				entry["now"] = cf.Path
			} else {
				entry["delta"] = "removed"
			}
			files = append(files, entry)
		}
		chapters = append(chapters, map[string]any{"title": ch.Title, "summary": ch.Summary, "files": files})
	}
	newPaths := make([]string, 0)
	for _, cf := range currentFiles {
		if !matched[cf.Path] {
			newPaths = append(newPaths, cf.Path)
		}
	}
	return map[string]any{
		"basis_version": basis.VersionNumber,
		"overview":      org.Overview,
		"chapters":      chapters,
		"new_paths":     newPaths,
	}, nil
}

// emitAIRequest appends an ai.request.* event carrying the full request object,
// stamped with the review's CURRENT version — not ar.VersionNumber, which is
// frozen at creation and goes stale once a later version lands.
func emitAIRequest(ctx context.Context, ap ccd.AppendFunc, origin, typ string, versionNumber int, ar store.AIRequest) {
	emit(ctx, ap, ar.ReviewID, origin, typ, versionNumber, map[string]any{"request": wire.ToAIRequest(ar)})
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

// carryOrganizationForward copies the review's latest organization onto v when
// v's path set and per-file fingerprints exactly match the organization's owning
// version: identical content tells the same story, so no re-organize is needed.
func carryOrganizationForward(ctx context.Context, st *store.Store, reviewID string, v store.Version, fingerprints map[string]string) (store.Organization, bool, error) {
	org, owner, ok, err := st.LatestOrganization(ctx, reviewID)
	if err != nil {
		return store.Organization{}, false, err
	}
	if !ok {
		return store.Organization{}, false, nil
	}
	ownerPrints, err := versionFingerprints(owner)
	if err != nil {
		return store.Organization{}, false, err
	}
	if !maps.Equal(ownerPrints, fingerprints) {
		return store.Organization{}, false, nil
	}
	if err := st.UpsertOrganization(ctx, v.ID, org); err != nil {
		return store.Organization{}, false, err
	}
	return org, true, nil
}

// openSystemOrganize returns the review's newest open (pending or working)
// system organize request.
func openSystemOrganize(ctx context.Context, st *store.Store, reviewID string) (store.AIRequest, bool, error) {
	requests, err := st.ListAIRequests(ctx, reviewID)
	if err != nil {
		return store.AIRequest{}, false, err
	}
	for _, ar := range requests {
		if ar.Source == store.OriginSystem && (ar.Status == "pending" || ar.Status == "working") {
			return ar, true, nil
		}
	}
	return store.AIRequest{}, false, nil
}

// openAIRequestsJSON returns the review's open requests as wire JSON, each
// byte-identical to its ai.request.created payload so the skill dedupes the
// redelivered offer by id.
func openAIRequestsJSON(ctx context.Context, st *store.Store, reviewID string, versionNumber int) ([]json.RawMessage, error) {
	open, err := st.ListOpenAIRequests(ctx, reviewID, versionNumber)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(open))
	for _, ar := range open {
		b, err := json.Marshal(wire.ToAIRequest(ar))
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// sweepStalePending fails user AI-bar requests left pending past stalePendingTTL:
// no live session dispatched them, so they would otherwise show "queued" forever.
// before is the staleness cutoff (sweepLoop passes now-stalePendingTTL).
func (rv *review) sweepStalePending(ctx context.Context, st *store.Store, ap ccd.AppendFunc, before time.Time) error {
	stale, err := st.StalePendingUserRequests(ctx, before)
	if err != nil {
		return err
	}
	for _, ar := range stale {
		updated, err := st.TransitionAIRequest(ctx, ar.ID, "failed",
			"Request expired — no live review session picked it up. Resume with /cc-review:start and retry.", nil)
		if err != nil {
			if errors.Is(err, store.ErrInvalidTransition) {
				continue
			}
			return err
		}
		v, ok, err := st.LatestVersion(ctx, ar.ReviewID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		emitAIRequest(ctx, ap, ccevent.OriginSystem, store.EventAIRequestUpdated, v.VersionNumber, updated)
	}
	return nil
}

// closeStaleOrganizeRequests marks the review's open system organize requests
// done with summary: no agent is coming to close them, and an open one keeps the
// UI's "organizing…" chip lit forever.
func closeStaleOrganizeRequests(ctx context.Context, st *store.Store, ap ccd.AppendFunc, reviewID string, versionNumber int, summary string) error {
	requests, err := st.ListAIRequests(ctx, reviewID)
	if err != nil {
		return err
	}
	for _, ar := range requests {
		if ar.Source != store.OriginSystem || (ar.Status != "pending" && ar.Status != "working") {
			continue
		}
		updated, err := st.TransitionAIRequest(ctx, ar.ID, "done", summary, nil)
		if err != nil {
			// A live agent's own update_ai_request can close the request between the
			// list above and this transition; already-closed is the goal state.
			if errors.Is(err, store.ErrInvalidTransition) {
				continue
			}
			return err
		}
		emitAIRequest(ctx, ap, ccevent.OriginSystem, store.EventAIRequestUpdated, versionNumber, updated)
	}
	return nil
}
