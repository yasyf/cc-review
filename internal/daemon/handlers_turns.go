package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	ccd "github.com/yasyf/cc-interact/daemon"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/attrib"
	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
)

// attributableWindow bounds how far back turns participate in attribution; once a
// repo has no turn inside it, the snapshot scratch state is swept.
const attributableWindow = 14 * 24 * time.Hour

// promptExcerptMax caps the stored prompt excerpt, in runes.
const promptExcerptMax = 1000

// sliceSchema is the versioned wire schema of `cc-transcript slice` lines.
const sliceSchema = "cc-transcript.slice/1"

// handleTurnStart opens a turn. An open review needs a bracket tight enough to
// place a bypassing write, so it pays a fresh snapshot; otherwise the previous
// turn's closing tree starts this one for no git, folding any edit made between
// the two into it. The cold path snapshots regardless: an empty TreeStart voids
// attribution for the whole version.
func (rv *review) handleTurnStart(hc ccd.HandlerCtx) ccd.Reply {
	ts := vcs.NewTurnStore(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	repoRoot := hc.Scope
	hc.RepoLock.Lock()
	defer hc.RepoLock.Unlock()
	// A still-open turn here means the prior Stop hook never fired (crash, kill):
	// it ends interrupted, and its writes are loose in the tree with no closing
	// snapshot to chain from.
	interrupted, err := ts.CloseOpenTurnsForWindow(hc.Ctx, repoRoot, hc.Window.ClaudePID)
	if err != nil {
		return errReply(err.Error())
	}
	turn := vcs.Turn{
		RepoRoot: repoRoot, SessionID: hc.Window.Session, ClaudePID: hc.Window.ClaudePID,
		PromptExcerpt: promptExcerpt(b.Prompt),
	}
	if interrupted == 0 && !reviewOpen(hc, repoRoot) {
		prev, backend, ok, err := chainTip(hc, ts, repoRoot)
		if err != nil {
			return errReply(err.Error())
		}
		if ok {
			turn.Backend, turn.TreeStart = backend, prev.TreeEnd
			if _, err := ts.CreateTurn(hc.Ctx, turn); err != nil {
				return errReply(err.Error())
			}
			return ccd.Reply{OK: true}
		}
	}
	sweepStaleScratch(hc.Ctx, ts, repoRoot)
	scratchDir, err := paths.App().EnsureRepoTurnsDir(repoRoot)
	if err != nil {
		return errReply(err.Error())
	}
	tree, err := vcs.SnapshotTree(hc.Ctx, repoRoot, scratchDir)
	if err != nil {
		return errReply(err.Error())
	}
	turn.Backend, turn.TreeStart = tree.Backend, tree.OID
	opened, err := ts.CreateTurn(hc.Ctx, turn)
	if err != nil {
		return errReply(err.Error())
	}
	rv.markSnapshotted(opened.ID)
	return ccd.Reply{OK: true}
}

func (rv *review) handleTurnEnd(hc ccd.HandlerCtx) ccd.Reply {
	ts := vcs.NewTurnStore(hc.DB)
	repoRoot := hc.Scope
	hc.RepoLock.Lock()
	defer hc.RepoLock.Unlock()
	turn, ok, err := ts.LatestOpenTurn(hc.Ctx, repoRoot, hc.Window.ClaudePID)
	if err != nil {
		return errReply(err.Error())
	}
	if !ok {
		return ccd.Reply{OK: true} // no open turn (e.g. daemon booted mid-turn)
	}
	scratchDir, err := paths.App().EnsureRepoTurnsDir(repoRoot)
	if err != nil {
		return errReply(err.Error())
	}
	tree, err := vcs.SnapshotTree(hc.Ctx, repoRoot, scratchDir)
	if err != nil {
		return errReply(err.Error())
	}
	if err := ts.CloseTurn(hc.Ctx, turn.ID, tree.OID, "closed"); err != nil {
		return errReply(err.Error())
	}
	if rv.takeSnapshotted(turn.ID) {
		rv.detectBypass(hc, repoRoot, scratchDir, turn, tree)
	}
	return ccd.Reply{OK: true}
}

// chainTip is the closed turn a new turn can start from, inside the attribution
// window. Another window mid-turn in the repo, or a tip from the other backend,
// means the tree can move outside this turn's bracket, so the caller snapshots
// instead of chaining.
func chainTip(hc ccd.HandlerCtx, ts *vcs.TurnStore, repoRoot string) (vcs.Turn, string, bool, error) {
	open, err := ts.OpenTurnCount(hc.Ctx, repoRoot)
	if err != nil {
		return vcs.Turn{}, "", false, err
	}
	if open > 0 {
		return vcs.Turn{}, "", false, nil
	}
	cutoff := time.Now().Add(-attributableWindow).UnixMilli()
	prev, ok, err := ts.LatestClosedTurn(hc.Ctx, repoRoot, cutoff)
	if err != nil || !ok {
		return vcs.Turn{}, "", false, err
	}
	backend, err := vcs.Backend(repoRoot)
	if err != nil {
		return vcs.Turn{}, "", false, err
	}
	if prev.Backend != backend {
		return vcs.Turn{}, "", false, nil
	}
	return prev, backend, true, nil
}

// reviewOpen reports whether the window has a review awaiting feedback here.
func reviewOpen(hc ccd.HandlerCtx, repoRoot string) bool {
	sub, ok, err := hc.Subjects.Find(hc.Ctx, hc.Window, repoRoot)
	return err == nil && ok && sub.Status == statusOpen
}

// markSnapshotted records a turn whose TreeStart was snapshotted at its own
// prompt, so its bracket holds only that turn's writes.
func (rv *review) markSnapshotted(turnID int64) {
	rv.snapshotMu.Lock()
	defer rv.snapshotMu.Unlock()
	if rv.snapshotted == nil {
		rv.snapshotted = make(map[int64]struct{})
	}
	rv.snapshotted[turnID] = struct{}{}
}

// takeSnapshotted consumes that mark. A chained turn, or one predating this
// daemon, reads false and bypass detection skips it rather than blaming it for
// writes its deferred TreeStart swept in.
func (rv *review) takeSnapshotted(turnID int64) bool {
	rv.snapshotMu.Lock()
	defer rv.snapshotMu.Unlock()
	_, ok := rv.snapshotted[turnID]
	delete(rv.snapshotted, turnID)
	return ok
}

// detectBypass flags tree changes during a locked-review turn that no logged tool
// call explains: the turn's changed files minus those a gate-allowed edit named
// minus those a sliced Bash command mentioned. Strictly non-fatal — the turn is
// already closed, the row is telemetry — and the wording never asserts who made
// the change.
func (rv *review) detectBypass(hc ccd.HandlerCtx, repoRoot, scratchDir string, turn vcs.Turn, treeEnd vcs.TreeRef) {
	if treeEnd.OID == turn.TreeStart {
		return
	}
	if !reviewOpen(hc, repoRoot) {
		return
	}
	changed, err := changedFiles(hc.Ctx, vcs.NewTreeDiffer(repoRoot, scratchDir, treeEnd.Backend), turn.TreeStart, treeEnd.OID)
	if err != nil {
		rv.log.Printf("bypass check: %v", err)
		return
	}
	nowMs := time.Now().UnixMilli()
	allowed := rv.gateAllowedFiles(turn, nowMs, repoRoot)
	summaries := rv.bashSummaries(hc.Ctx, turn.SessionID, turn.StartedAt, nowMs)
	var remaining, attributed []string
	for _, f := range changed {
		if allowed[f] || mentionedInBash(summaries, f) {
			attributed = append(attributed, f)
		} else {
			remaining = append(remaining, f)
		}
	}
	if len(remaining) == 0 {
		return
	}
	detail, _ := json.Marshal(map[string]any{
		"changed_files": remaining, "attributed_files": attributed, "turn_id": turn.ID,
	})
	if err := rv.decisions.Append(decisions.Decision{
		TsMs: nowMs, SessionID: turn.SessionID, Source: "cc-review", Kind: "bypass-detected",
		Event: "Stop", Action: "note",
		Message:    fmt.Sprintf("%d file(s) changed during a locked review turn with no logged tool call naming them", len(remaining)),
		DetailJSON: string(detail),
	}); err != nil {
		rv.log.Printf("bypass check: append: %v", err)
	}
}

func changedFiles(ctx context.Context, d vcs.TreeDiffer, from, to string) ([]string, error) {
	patch, err := d.Diff(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("diff %s..%s: %w", from, to, err)
	}
	files, _, err := gitdiff.Parse(strings.NewReader(patch))
	if err != nil {
		return nil, fmt.Errorf("parse %s..%s patch: %w", from, to, err)
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f.IsDelete {
			out = append(out, f.OldName)
			continue
		}
		out = append(out, f.NewName)
	}
	return out, nil
}

// gateAllowedFiles is the repo-relative set of files the turn's gate-allowed
// edits named; blocked calls never ran, so they explain nothing.
func (rv *review) gateAllowedFiles(turn vcs.Turn, untilMs int64, repoRoot string) map[string]bool {
	rows, err := rv.decisions.ForTurn(turn.SessionID, turn.StartedAt, untilMs)
	if err != nil {
		rv.log.Printf("bypass check: decisions: %v", err)
		return nil
	}
	files := make(map[string]bool)
	for _, d := range rows {
		if d.Source != "cc-review" || d.Kind != "gate" || d.Action != "allow" {
			continue
		}
		var detail struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(d.DetailJSON), &detail) == nil && detail.FilePath != "" {
			files[relToRepo(repoRoot, detail.FilePath)] = true
		}
	}
	return files
}

func relToRepo(repoRoot, path string) string {
	return strings.TrimPrefix(path, repoRoot+string(filepath.Separator))
}

// bashSummaries shells out to `cc-transcript slice` for the turn window and
// returns the rendered Bash commands it saw. A missing binary, a nonzero exit
// (1 = transcript missing), or schema skew all mean "no slice data": the Bash
// subtraction is skipped and the degradation logged once per daemon.
func (rv *review) bashSummaries(ctx context.Context, sessionID string, sinceMs, untilMs int64) []string {
	out, err := exec.CommandContext(ctx, "cc-transcript", "slice", //nolint:gosec // G204: fixed cc-transcript subcommand; args are this tool's own session ID and timestamps, intentionally shelling out to the companion CLI.
		"--session", sessionID, "--since", rfc3339Ms(sinceMs), "--until", rfc3339Ms(untilMs)).Output()
	if err != nil {
		rv.warnNoSlice(err.Error())
		return nil
	}
	var summaries []string
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			Schema   string `json:"schema"`
			ToolName string `json:"tool_name"`
			Summary  string `json:"summary"`
		}
		if err := json.Unmarshal(line, &row); err != nil || row.Schema != sliceSchema {
			rv.warnNoSlice(fmt.Sprintf("unexpected slice line %q", line))
			return nil
		}
		if row.ToolName == "Bash" {
			summaries = append(summaries, row.Summary)
		}
	}
	return summaries
}

func (rv *review) warnNoSlice(cause string) {
	rv.sliceWarn.Do(func() {
		rv.log.Printf("bypass check: cc-transcript slice unavailable (%s); skipping Bash attribution", cause)
	})
}

func mentionedInBash(summaries []string, relPath string) bool {
	base := filepath.Base(relPath)
	for _, summary := range summaries {
		if strings.Contains(summary, base) {
			return true
		}
	}
	return false
}

func rfc3339Ms(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

// sweepStaleScratch wipes a repo's snapshot scratch objects and index once no
// turn is inside the attribution window — the orphaned objects back nothing the
// differ will ever read, and the next snapshot reseeds from the repo's real
// index. Turn rows are never deleted; they back display of old versions.
func sweepStaleScratch(ctx context.Context, ts *vcs.TurnStore, repoRoot string) {
	cutoff := time.Now().Add(-attributableWindow).UnixMilli()
	turns, err := ts.ListAttributableTurns(ctx, repoRoot, cutoff)
	if err != nil || len(turns) > 0 {
		return
	}
	dir := paths.App().RepoTurnsDir(repoRoot)
	_ = os.RemoveAll(filepath.Join(dir, "objects"))
	_ = os.Remove(filepath.Join(dir, "index"))
}

// attributeVersion tags the pending section's added lines with the turns that
// wrote them. Strictly non-fatal: any failure logs and leaves the section
// unattributed. The caller holds the repo lock, so the tree snapshotted here is
// the one the section's patch captured.
func (rv *review) attributeVersion(ctx context.Context, st *store.Store, repoRoot string, sectionID int64, patchText string) {
	ts := vcs.NewTurnStore(st.DB())
	scratchDir, err := paths.App().EnsureRepoTurnsDir(repoRoot)
	if err != nil {
		rv.log.Printf("attribution: %v", err)
		return
	}
	treeNow, err := vcs.SnapshotTree(ctx, repoRoot, scratchDir)
	if err != nil {
		rv.log.Printf("attribution: snapshot tree: %v", err)
		return
	}
	cutoff := time.Now().Add(-attributableWindow).UnixMilli()
	turns, err := ts.ListAttributableTurns(ctx, repoRoot, cutoff)
	if err != nil {
		rv.log.Printf("attribution: %v", err)
		return
	}
	chain, tagged := snapshotChain(turns, treeNow)
	if !tagged {
		return
	}
	byFile, err := attrib.Compute(ctx, vcs.NewTreeDiffer(repoRoot, scratchDir, treeNow.Backend), chain, patchText)
	if err != nil {
		rv.log.Printf("attribution: compute: %v", err)
		return
	}
	if len(byFile) == 0 {
		return
	}
	if err := st.PutAttributions(ctx, sectionID, byFile); err != nil {
		rv.log.Printf("attribution: %v", err)
	}
}

// snapshotChain orders a repo's turns into a contiguous tree-transition chain
// ending at treeNow. Untagged gap links absorb manual edits between turns and the
// edits of interrupted turns (which have no closing snapshot); a still-open turn
// is virtually closed at treeNow without touching the DB. tagged is false when no
// turn link made the chain.
func snapshotChain(turns []vcs.Turn, treeNow vcs.TreeRef) (chain []attrib.Link, tagged bool) {
	tip := ""
	for _, t := range turns {
		if t.Backend != treeNow.Backend || t.Status == "interrupted" {
			continue
		}
		if tip != "" && tip != t.TreeStart {
			chain = append(chain, attrib.Link{From: tip, To: t.TreeStart})
		}
		to := t.TreeEnd
		if t.Status == "open" {
			to = treeNow.OID
		}
		chain = append(chain, attrib.Link{From: t.TreeStart, To: to, TurnID: t.ID})
		tagged = true
		tip = to
	}
	if tip != "" && tip != treeNow.OID {
		chain = append(chain, attrib.Link{From: tip, To: treeNow.OID})
	}
	return chain, tagged
}

func promptExcerpt(prompt string) string {
	runes := []rune(prompt)
	if len(runes) <= promptExcerptMax {
		return prompt
	}
	return string(runes[:promptExcerptMax])
}
