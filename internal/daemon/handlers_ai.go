package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
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

// reviewWithSections resolves the window's review, its latest version, and that
// version's sections; a non-nil Reply is the failure to return as-is.
func reviewWithSections(hc ccd.HandlerCtx, st *store.Store) (subject.Subject, store.Version, []store.Section, *ccd.Reply) {
	sub, v, fail := reviewWithLatest(hc, st)
	if fail != nil {
		return subject.Subject{}, store.Version{}, nil, fail
	}
	sections, err := st.ListSections(hc.Ctx, v.ID)
	if err != nil {
		r := errReply(err.Error())
		return subject.Subject{}, store.Version{}, nil, &r
	}
	return sub, v, sections, nil
}

func (rv *review) handleFileStates(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	if len(b.Files) == 0 {
		return errReply("file-states requires at least one file")
	}
	sub, v, sections, fail := reviewWithSections(hc, st)
	if fail != nil {
		return *fail
	}
	fingerprints, err := versionSectionFingerprints(sections)
	if err != nil {
		return errReply(err.Error())
	}
	inputs := make([]store.FileStateInput, 0, len(b.Files))
	reasons := make([]string, 0, len(b.Files))
	for _, f := range b.Files {
		inputs = append(inputs, store.FileStateInput{SectionKey: f.SectionKey, Path: f.Path, Reviewed: f.Reviewed, Hidden: f.Hidden})
		reasons = append(reasons, f.Reason)
	}
	return rv.applyFileStateBatch(hc, st, sub, v, fingerprints, inputs, reasons, b.AIRequestID)
}

// handleFileStatesByRisk flips every file the current organization tags with one
// of the requested risk levels to the given reviewed/hidden state — the server
// resolves the path set from the organization, so the agent never enumerates
// paths (the filter-and-flip shortcut behind "mark all mechanical as viewed").
func (rv *review) handleFileStatesByRisk(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	if len(b.Risk) == 0 {
		return errReply("file-states-by-risk requires at least one risk level")
	}
	if b.Reviewed == nil && b.Hidden == nil {
		return errReply("file-states-by-risk requires reviewed or hidden to set")
	}
	sub, v, sections, fail := reviewWithSections(hc, st)
	if fail != nil {
		return *fail
	}
	orgs, err := st.GetOrganizationsByVersion(hc.Ctx, v.ID)
	if err != nil {
		return errReply(err.Error())
	}
	if len(orgs) == 0 {
		return errReply("no organization yet — the review must be organized before selecting files by risk")
	}
	fingerprints, err := versionSectionFingerprints(sections)
	if err != nil {
		return errReply(err.Error())
	}
	want := make(map[string]bool, len(b.Risk))
	for _, r := range b.Risk {
		want[r] = true
	}
	seen := make(map[store.SectionFileKey]bool)
	inputs := make([]store.FileStateInput, 0)
	reasons := make([]string, 0)
	matched := make([]string, 0)
	for _, sec := range sections {
		org, ok := orgs[sec.ID]
		if !ok {
			continue
		}
		for _, ch := range org.Chapters {
			for _, f := range ch.Files {
				key := store.SectionFileKey{SectionKey: sec.Key(), Path: f.Path}
				if !want[f.Risk] || seen[key] {
					continue
				}
				// Only flip paths the current section still carries; a stale org's
				// moved/removed file is left for the agent to re-organize.
				if _, inSection := fingerprints[key]; !inSection {
					continue
				}
				seen[key] = true
				inputs = append(inputs, store.FileStateInput{SectionKey: sec.Key(), Path: f.Path, Reviewed: b.Reviewed, Hidden: b.Hidden})
				reasons = append(reasons, b.Reason)
				matched = append(matched, sectionFileLabel(sec.Key(), f.Path))
			}
		}
	}
	if len(inputs) == 0 {
		return okReply(result{Paths: []string{}})
	}
	if reply := rv.applyFileStateBatch(hc, st, sub, v, fingerprints, inputs, reasons, b.AIRequestID); !reply.OK {
		return reply
	}
	return okReply(result{Paths: matched})
}

// sectionFileLabel qualifies a path with its section branch when the file lives
// on a committed stack section; the pending section's files stay bare.
func sectionFileLabel(sectionKey, path string) string {
	if sectionKey == "" {
		return path
	}
	return sectionKey + ":" + path
}

// applyFileStateBatch validates, applies, records (when tied to an AI request),
// and emits one batch of per-file state changes. inputs and reasons are parallel;
// aiRequestID of 0 means no AI request. Paths absent from the current version are
// rejected with the unknown list.
func (rv *review) applyFileStateBatch(hc ccd.HandlerCtx, st *store.Store, sub subject.Subject, v store.Version, fingerprints map[store.SectionFileKey]string, inputs []store.FileStateInput, reasons []string, aiRequestID int64) ccd.Reply {
	var unknown []string
	for _, in := range inputs {
		if _, ok := fingerprints[store.SectionFileKey{SectionKey: in.SectionKey, Path: in.Path}]; !ok {
			unknown = append(unknown, sectionFileLabel(in.SectionKey, in.Path))
		}
	}
	if len(unknown) > 0 {
		return errReply(fmt.Sprintf("unknown paths (not in version %d): %s", v.VersionNumber, strings.Join(unknown, ", ")))
	}
	if aiRequestID != 0 {
		ar, err := st.GetAIRequest(hc.Ctx, aiRequestID)
		if err != nil {
			return errReply(err.Error())
		}
		if ar.ReviewID != sub.ID {
			return errReply(fmt.Sprintf("ai request %d does not belong to this review", aiRequestID))
		}
		if !isOpenAIStatus(ar.Status) {
			return errReply(fmt.Sprintf("ai request %d is %q: changes are only recorded while pending or working", aiRequestID, ar.Status))
		}
	}
	results, err := st.ApplyFileStates(hc.Ctx, sub.ID, inputs, fingerprints)
	if err != nil {
		return errReply(err.Error())
	}
	if aiRequestID != 0 {
		changes := make([]store.AIChange, 0, len(results))
		for i, res := range results {
			changes = append(changes, store.AIChange{
				SectionKey: res.SectionKey, Path: res.Path, Reason: reasons[i], Prior: res.Prior, Applied: res.Applied,
			})
		}
		if err := st.AppendAIRequestChanges(hc.Ctx, aiRequestID, changes); err != nil {
			return errReply(err.Error())
		}
	}
	states := make([]map[string]any, 0, len(results))
	for i, res := range results {
		s := map[string]any{"sectionKey": res.SectionKey, "path": res.Path, "reviewed": res.Applied.Reviewed, "hidden": res.Applied.Hidden}
		if reasons[i] != "" {
			s["reason"] = reasons[i]
		}
		states = append(states, s)
	}
	fields := map[string]any{"states": states}
	if aiRequestID != 0 {
		fields["aiRequestId"] = strconv.FormatInt(aiRequestID, 10)
	}
	// Apply and emit are not atomic: racing with the REST file-states handler,
	// same-path events can land in the opposite order of the DB applies. Accepted
	// — the session GET is authoritative, so replay converges on the next load.
	emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginAgent, store.EventFileStates, v.VersionNumber, fields)
	return ccd.Reply{OK: true}
}

// isOpenAIStatus reports whether an AI request is in a state that still records
// file-state changes: pending or working (a parked awaiting_input question does
// not, and answered is the resumable hand-off, not an active worker).
func isOpenAIStatus(status string) bool {
	return status == "pending" || status == "working"
}

func (rv *review) handleUpdateAIRequest(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	switch b.AIStatus {
	case "working", "done", "failed", "awaiting_input":
	default:
		return errReply(fmt.Sprintf("update-ai-request status %q: want working | done | failed | awaiting_input", b.AIStatus))
	}
	if b.AIStatus == "awaiting_input" {
		if b.Question == "" {
			return errReply("awaiting_input needs a question")
		}
		if b.Ask != nil {
			if err := b.Ask.Validate(); err != nil {
				return errReply(err.Error())
			}
		}
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
	var updated store.AIRequest
	switch b.AIStatus {
	case "awaiting_input":
		updated, err = st.AskAIRequest(hc.Ctx, b.AIRequestID, store.AIQuestion{Body: b.Question, Ask: b.Ask})
	case "working":
		updated, err = st.MarkWorking(hc.Ctx, b.AIRequestID, b.Phase)
	default:
		updated, err = st.TransitionAIRequest(hc.Ctx, b.AIRequestID, b.AIStatus, b.Summary, b.Unmatched)
	}
	if err != nil {
		return errReply(err.Error())
	}
	emitAIRequest(hc.Ctx, hc.Append, ccevent.OriginAgent, store.EventAIRequestUpdated, v.VersionNumber, updated)
	return ccd.Reply{OK: true}
}

func (rv *review) handleSubmitOrganization(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	if b.Organization == nil {
		return errReply("submit-organization requires chapters")
	}
	sub, v, sections, fail := reviewWithSections(hc, st)
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
	sec, ok := sectionsByKey(sections)[b.SectionKey]
	if !ok {
		return errReply(fmt.Sprintf("unknown section %q — take section_key from get_review_files", b.SectionKey))
	}
	files, err := sec.Files()
	if err != nil {
		return errReply(err.Error())
	}
	filePaths := make([]string, 0, len(files))
	for _, f := range files {
		filePaths = append(filePaths, f.Path)
	}
	validate := b.Organization.Validate
	if b.Partial {
		validate = b.Organization.ValidatePartial
	}
	if err := validate(filePaths); err != nil {
		return errReply(err.Error())
	}
	if err := st.UpsertOrganization(hc.Ctx, sec.ID, *b.Organization); err != nil {
		return errReply(err.Error())
	}
	emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginAgent, store.EventOrganizationUpdated, v.VersionNumber,
		map[string]any{"sectionKey": sec.Key(), "organization": *b.Organization})
	return ccd.Reply{OK: true}
}

// handleAnnotate writes Claude-authored line annotations onto the current
// version's diff: highlights (the new annotations table) and comments (a
// Claude-authored comment thread). Both are informational — they never gate the
// reviewer's submit. AIRequestID ties highlights to the request for undo.
func (rv *review) handleAnnotate(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	if len(b.Annotations) == 0 {
		return errReply("annotate requires at least one annotation")
	}
	sub, v, sections, fail := reviewWithSections(hc, st)
	if fail != nil {
		return *fail
	}
	byKey := sectionsByKey(sections)
	fingerprints, err := versionSectionFingerprints(sections)
	if err != nil {
		return errReply(err.Error())
	}
	if b.AIRequestID != 0 {
		ar, err := st.GetAIRequest(hc.Ctx, b.AIRequestID)
		if err != nil {
			return errReply(err.Error())
		}
		if ar.ReviewID != sub.ID {
			return errReply(fmt.Sprintf("ai request %d does not belong to this review", b.AIRequestID))
		}
		if !isOpenAIStatus(ar.Status) {
			return errReply(fmt.Sprintf("ai request %d is %q: annotations are only recorded while pending or working", b.AIRequestID, ar.Status))
		}
	}
	for i, a := range b.Annotations {
		switch a.Kind {
		case "highlight", "comment":
		default:
			return errReply(fmt.Sprintf("annotation %d: kind %q (want highlight | comment)", i, a.Kind))
		}
		if a.Side != "additions" && a.Side != "deletions" {
			return errReply(fmt.Sprintf("annotation %d: side %q (want additions | deletions)", i, a.Side))
		}
		if _, ok := byKey[a.SectionKey]; !ok {
			return errReply(fmt.Sprintf("annotation %d: unknown section %q", i, a.SectionKey))
		}
		if _, ok := fingerprints[store.SectionFileKey{SectionKey: a.SectionKey, Path: a.FilePath}]; !ok {
			return errReply(fmt.Sprintf("annotation %d: unknown path %q (not in section %q)", i, a.FilePath, a.SectionKey))
		}
		if a.StartLine < 1 || a.EndLine < a.StartLine {
			return errReply(fmt.Sprintf("annotation %d: bad line range %d-%d", i, a.StartLine, a.EndLine))
		}
		if a.Kind == "comment" && a.Body == "" {
			return errReply(fmt.Sprintf("annotation %d: a comment annotation needs a body", i))
		}
	}
	createdHighlight := false
	for _, a := range b.Annotations {
		sec := byKey[a.SectionKey]
		if a.Kind == "highlight" {
			if _, err := st.CreateAnnotation(hc.Ctx, store.Annotation{
				VersionID: v.ID, SectionID: sec.ID, FilePath: a.FilePath, Side: a.Side,
				StartLine: a.StartLine, EndLine: a.EndLine, Label: a.Body, AIRequestID: b.AIRequestID,
			}); err != nil {
				return errReply(err.Error())
			}
			createdHighlight = true
			continue
		}
		cid, err := st.CreateComment(hc.Ctx, store.Comment{
			VersionID: v.ID, SectionID: sec.ID, Branch: sec.Key(), Pending: sec.Pending,
			FilePath: a.FilePath, Side: a.Side,
			StartLine: a.StartLine, EndLine: a.EndLine, Body: a.Body, Author: store.OriginClaude, Status: "open",
		})
		if err != nil {
			if errors.Is(err, store.ErrStaleSection) {
				return errReply("the diff changed while annotating — re-run get_review_files and retry")
			}
			return errReply(err.Error())
		}
		c, err := st.GetComment(hc.Ctx, cid)
		if err != nil {
			return errReply(err.Error())
		}
		emit(hc.Ctx, hc.Append, sub.ID, ccevent.OriginAgent, store.EventCommentCreated, v.VersionNumber,
			map[string]any{"commentId": strconv.FormatInt(cid, 10), "comment": wire.ToComment(c, nil)})
	}
	if createdHighlight {
		if err := emitAnnotations(hc.Ctx, hc.Append, st, sub.ID, v); err != nil {
			return errReply(err.Error())
		}
	}
	return ccd.Reply{OK: true}
}

// emitAnnotations broadcasts the version's full highlight list (idempotent, like
// organization.updated) so the browser replaces its annotation set.
func emitAnnotations(ctx context.Context, ap ccd.AppendFunc, st *store.Store, reviewID string, v store.Version) error {
	list, err := st.ListAnnotationsByVersion(ctx, v.ID)
	if err != nil {
		return err
	}
	sections, err := st.ListSections(ctx, v.ID)
	if err != nil {
		return err
	}
	keyByID := make(map[int64]string, len(sections))
	for _, sec := range sections {
		keyByID[sec.ID] = sec.Key()
	}
	wired := make([]wire.Annotation, 0, len(list))
	for _, a := range list {
		wired = append(wired, wire.ToAnnotation(a, keyByID[a.SectionID]))
	}
	emit(ctx, ap, reviewID, ccevent.OriginAgent, store.EventAnnotationsUpdated, v.VersionNumber,
		map[string]any{"annotations": wired})
	return nil
}

// reviewFilesInlineCap bounds how many file entries get_review_files inlines.
// Past it the agent reads review_files_path instead, so a large review can't blow
// the tool-result token budget.
const reviewFilesInlineCap = 50

func (rv *review) handleReviewFiles(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	sub, v, sections, fail := reviewWithSections(hc, st)
	if fail != nil {
		return *fail
	}
	meta, _, err := st.GetReviewMeta(hc.Ctx, sub.ID)
	if err != nil {
		return errReply(err.Error())
	}
	states, err := st.ListFileStates(hc.Ctx, sub.ID)
	if err != nil {
		return errReply(err.Error())
	}
	byKey := make(map[store.SectionFileKey]store.FileState, len(states))
	for _, s := range states {
		byKey[store.SectionFileKey{SectionKey: s.SectionKey, Path: s.Path}] = s
	}
	orgs, err := st.GetOrganizationsByVersion(hc.Ctx, v.ID)
	if err != nil {
		return errReply(err.Error())
	}
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	filtering := b.Status != "" || b.Reviewed != nil || b.Hidden != nil
	sectionsOut := make([]map[string]any, 0, len(sections))
	for _, sec := range sections {
		files, err := sec.FileFlags()
		if err != nil {
			return errReply(err.Error())
		}
		entries := make([]map[string]any, 0, len(files))
		selected := make([]map[string]any, 0)
		for _, f := range files {
			fs := byKey[store.SectionFileKey{SectionKey: sec.Key(), Path: f.Path}]
			e := map[string]any{
				"path": f.Path, "status": f.Status,
				"reviewed": fs.Reviewed, "hidden": fs.Hidden,
				"generated": f.Generated, "vendored": f.Vendored,
			}
			if f.OldPath != "" {
				e["old_path"] = f.OldPath
			}
			entries = append(entries, e)
			if matchesReviewFilter(f.FileChange, fs, b) {
				selected = append(selected, e)
			}
		}
		filesPath, err := writeReviewFilesList(sec.PatchPath, entries)
		if err != nil {
			return errReply(err.Error())
		}
		out := map[string]any{
			"section_key":       sec.Key(),
			"position":          sec.Position,
			"branch":            sec.Branch,
			"parent_branch":     sec.ParentBranch,
			"pending":           sec.Pending,
			"patch_path":        sec.PatchPath,
			"review_files_path": filesPath,
			"organized":         organizedHas(orgs, sec.ID),
		}
		// Rebuild context: the latest same-key organization, delta-annotated
		// against this section, so the agent re-organizes incrementally.
		if org, owner, ok, err := st.LatestOrganizationForKey(hc.Ctx, sub.ID, sec.Key()); err != nil {
			return errReply(err.Error())
		} else if ok {
			ownerVer, err := st.GetVersionByID(hc.Ctx, owner.VersionID)
			if err != nil {
				return errReply(err.Error())
			}
			block, err := organizationContext(org, owner, sec, ownerVer.VersionNumber)
			if err != nil {
				return errReply(err.Error())
			}
			orgPath, err := writeReviewOrganization(sec.PatchPath, block)
			if err != nil {
				return errReply(err.Error())
			}
			out["organization_path"] = orgPath
		}
		if filtering {
			out["match_count"] = len(selected)
		}
		if len(selected) <= reviewFilesInlineCap {
			out["files"] = selected
		}
		sectionsOut = append(sectionsOut, out)
	}
	raw, err := json.Marshal(map[string]any{
		"version_number": v.VersionNumber,
		"stack":          meta.Stack,
		"sections":       sectionsOut,
	})
	if err != nil {
		return errReply(err.Error())
	}
	return okReply(result{ReviewFiles: raw})
}

// organizedHas reports whether a section has its own organization this version.
func organizedHas(orgs map[int64]store.Organization, sectionID int64) bool {
	_, ok := orgs[sectionID]
	return ok
}

// matchesReviewFilter reports whether a file passes the get_review_files filter;
// an empty/nil dimension is unconstrained, so an empty filter matches everything.
func matchesReviewFilter(f vcs.FileChange, fs store.FileState, b body) bool {
	if b.Status != "" && f.Status != b.Status {
		return false
	}
	if b.Reviewed != nil && fs.Reviewed != *b.Reviewed {
		return false
	}
	if b.Hidden != nil && fs.Hidden != *b.Hidden {
		return false
	}
	return true
}

// writeReviewFilesList writes the full file listing as JSONL (one entry per line,
// Read-with-offset / Grep friendly) next to the version's patch, returning its path.
func writeReviewFilesList(patchPath string, entries []map[string]any) (string, error) {
	path := strings.TrimSuffix(patchPath, ".patch") + ".files.jsonl"
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return "", fmt.Errorf("encode review file entry: %w", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return "", fmt.Errorf("write review files list: %w", err)
	}
	return path, nil
}

// writeReviewOrganization writes the annotated organization as indented JSON next
// to the version's patch, returning its path.
func writeReviewOrganization(patchPath string, block map[string]any) (string, error) {
	path := strings.TrimSuffix(patchPath, ".patch") + ".org.json"
	data, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode organization: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write organization: %w", err)
	}
	return path, nil
}

// organizationContext annotates the latest organization against the current
// version so the organize agent rebuilds incrementally instead of from scratch:
// per file a delta mark (absent = unchanged diff), plus the current paths the
// basis never covered. Files are joined by path first, then by base origin —
// every version diffs the review's pinned base, so a rename's old_path keys it
// back to the basis entry. A direct path match always wins over an origin join.
func organizationContext(org store.Organization, basis, current store.Section, basisVersion int) (map[string]any, error) {
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
			entry := map[string]any{"path": f.Path, "risk": f.Risk, "rationale": f.Rationale, "focus": f.Focus}
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
			// New-side line numbers only hold for files whose diff is unchanged; for
			// changed/moved/removed files they are stale, so omit them.
			if _, hasDelta := entry["delta"]; !hasDelta && len(f.Lines) > 0 {
				entry["lines"] = f.Lines
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
		"basis_version": basisVersion,
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

// sectionFingerprints maps a section's paths to their diff fingerprints.
func sectionFingerprints(sec store.Section) (map[string]string, error) {
	files, err := sec.Files()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = f.Fingerprint
	}
	return out, nil
}

// versionSectionFingerprints maps every (sectionKey, path) in a version's
// sections to its diff fingerprint.
func versionSectionFingerprints(sections []store.Section) (map[store.SectionFileKey]string, error) {
	out := make(map[store.SectionFileKey]string)
	for _, sec := range sections {
		prints, err := sectionFingerprints(sec)
		if err != nil {
			return nil, err
		}
		for path, fp := range prints {
			out[store.SectionFileKey{SectionKey: sec.Key(), Path: path}] = fp
		}
	}
	return out, nil
}

// sectionsByKey indexes a version's sections by their stable key.
func sectionsByKey(sections []store.Section) map[string]store.Section {
	out := make(map[string]store.Section, len(sections))
	for _, sec := range sections {
		out[sec.Key()] = sec
	}
	return out
}

// allSectionsOrganized reports whether every section of a version has an
// organization.
func allSectionsOrganized(ctx context.Context, st *store.Store, sections []store.Section) (bool, error) {
	if len(sections) == 0 {
		return false, nil
	}
	orgs, err := st.GetOrganizationsByVersion(ctx, sections[0].VersionID)
	if err != nil {
		return false, err
	}
	for _, sec := range sections {
		if _, ok := orgs[sec.ID]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// carrySectionOrganizations copies each section's latest same-key organization
// forward when the section's content is byte-identical, returning the carried
// orgs keyed by section id and whether every section was carried.
func (rv *review) carrySectionOrganizations(ctx context.Context, st *store.Store, reviewID string, sections []store.Section) (map[int64]store.Organization, bool, error) {
	carried := make(map[int64]store.Organization)
	all := true
	for _, sec := range sections {
		org, ok, err := carrySectionOrganizationForward(ctx, st, reviewID, sec)
		if err != nil {
			return nil, false, err
		}
		if ok {
			carried[sec.ID] = org
		} else {
			all = false
		}
	}
	return carried, all, nil
}

// carrySectionOrganizationForward copies the section's latest same-key
// organization onto it when their per-file fingerprints match exactly: identical
// content tells the same story, so no re-organize is needed.
func carrySectionOrganizationForward(ctx context.Context, st *store.Store, reviewID string, sec store.Section) (store.Organization, bool, error) {
	org, owner, ok, err := st.LatestOrganizationForKey(ctx, reviewID, sec.Key())
	if err != nil {
		return store.Organization{}, false, err
	}
	if !ok {
		return store.Organization{}, false, nil
	}
	ownerPrints, err := sectionFingerprints(owner)
	if err != nil {
		return store.Organization{}, false, err
	}
	prints, err := sectionFingerprints(sec)
	if err != nil {
		return store.Organization{}, false, err
	}
	if !maps.Equal(ownerPrints, prints) {
		return store.Organization{}, false, nil
	}
	if err := st.UpsertOrganization(ctx, sec.ID, org); err != nil {
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

// failStrandedQuestions fails user requests parked on a clarifying question for a
// version other than the current one: the version-scoped open set never re-offers
// them and the sweeper only touches pending, so they would orphan otherwise.
func failStrandedQuestions(ctx context.Context, st *store.Store, ap ccd.AppendFunc, reviewID string, versionNumber int) error {
	parked, err := st.AwaitingInputRequests(ctx, reviewID)
	if err != nil {
		return err
	}
	for _, ar := range parked {
		if ar.VersionNumber == versionNumber {
			continue
		}
		updated, err := st.TransitionAIRequest(ctx, ar.ID, "failed",
			"Question abandoned — the diff changed before you answered; re-ask in the AI bar.", nil)
		if err != nil {
			if errors.Is(err, store.ErrInvalidTransition) {
				continue
			}
			return err
		}
		emitAIRequest(ctx, ap, ccevent.OriginSystem, store.EventAIRequestUpdated, versionNumber, updated)
	}
	return nil
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
