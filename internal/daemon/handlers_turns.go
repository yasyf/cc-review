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
	"github.com/yasyf/cc-review/internal/attrib"
	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/vcs"
)

// attributableWindow bounds how far back turns participate in attribution; once
// a repo has no turn inside it, the snapshot scratch state is swept.
const attributableWindow = 14 * 24 * time.Hour

// promptExcerptMax caps the stored prompt excerpt, in runes.
const promptExcerptMax = 1000

// sliceSchema is the versioned wire schema of `cc-transcript slice` lines.
const sliceSchema = "cc-transcript.slice/1"

func (s *Server) handleTurnStart(ctx context.Context, req Request) Response {
	repoRoot, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true} // not a repo: nothing to record
	}
	mu := s.repoLock(repoRoot)
	mu.Lock()
	defer mu.Unlock()
	s.sweepStaleScratch(ctx, repoRoot)
	scratchDir, err := paths.EnsureRepoTurnsDir(repoRoot)
	if err != nil {
		return errResp(err.Error())
	}
	tree, err := vcs.SnapshotTree(ctx, repoRoot, scratchDir)
	if err != nil {
		return errResp(err.Error())
	}
	// A still-open turn here means the prior Stop hook never fired (crash,
	// kill): no closing snapshot exists, so it ends interrupted.
	if err := s.store.CloseOpenTurnsForWindow(ctx, repoRoot, req.ClaudePID); err != nil {
		return errResp(err.Error())
	}
	if _, err := s.store.CreateTurn(ctx, store.Turn{
		RepoRoot: repoRoot, Backend: tree.Backend, SessionID: req.Session, ClaudePID: req.ClaudePID,
		PromptExcerpt: promptExcerpt(req.Prompt), TreeStart: tree.OID,
	}); err != nil {
		return errResp(err.Error())
	}
	return Response{OK: true}
}

func (s *Server) handleTurnEnd(ctx context.Context, req Request) Response {
	repoRoot, err := vcs.Root(ctx, req.Cwd)
	if err != nil {
		return Response{OK: true} // not a repo: nothing to record
	}
	mu := s.repoLock(repoRoot)
	mu.Lock()
	defer mu.Unlock()
	turn, ok, err := s.store.LatestOpenTurn(ctx, repoRoot, req.ClaudePID)
	if err != nil {
		return errResp(err.Error())
	}
	if !ok {
		return Response{OK: true} // no open turn (e.g. daemon booted mid-turn)
	}
	scratchDir, err := paths.EnsureRepoTurnsDir(repoRoot)
	if err != nil {
		return errResp(err.Error())
	}
	tree, err := vcs.SnapshotTree(ctx, repoRoot, scratchDir)
	if err != nil {
		return errResp(err.Error())
	}
	if err := s.store.CloseTurn(ctx, turn.ID, tree.OID, "closed"); err != nil {
		return errResp(err.Error())
	}
	s.detectBypass(ctx, req, repoRoot, scratchDir, turn, tree)
	return Response{OK: true}
}

// detectBypass flags tree changes during a locked-review turn that no logged
// tool call explains: the turn's changed files minus those a gate-allowed
// edit named minus those a sliced Bash command mentioned. Strictly non-fatal
// — the turn is already closed, the row is telemetry — and the wording never
// asserts who made the change.
func (s *Server) detectBypass(ctx context.Context, req Request, repoRoot, scratchDir string, turn store.Turn, treeEnd vcs.TreeRef) {
	if treeEnd.OID == turn.TreeStart {
		return
	}
	review, ok, err := s.resolver.Find(ctx, win(req), repoRoot)
	if err != nil || !ok || review.Status != "open" {
		return
	}
	changed, err := changedFiles(ctx, vcs.NewTreeDiffer(repoRoot, scratchDir, treeEnd.Backend), turn.TreeStart, treeEnd.OID)
	if err != nil {
		s.log.Printf("bypass check: %v", err)
		return
	}
	nowMs := time.Now().UnixMilli()
	allowed := s.gateAllowedFiles(turn, nowMs, repoRoot)
	summaries := s.bashSummaries(ctx, turn.SessionID, turn.StartedAt, nowMs)
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
	if err := s.decisions.Append(decisions.Decision{
		TsMs: nowMs, SessionID: turn.SessionID, Source: "cc-review", Kind: "bypass-detected",
		Event: "Stop", Action: "note",
		Message:    fmt.Sprintf("%d file(s) changed during a locked review turn with no logged tool call naming them", len(remaining)),
		DetailJSON: string(detail),
	}); err != nil {
		s.log.Printf("bypass check: append: %v", err)
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
func (s *Server) gateAllowedFiles(turn store.Turn, untilMs int64, repoRoot string) map[string]bool {
	rows, err := s.decisions.ForTurn(turn.SessionID, turn.StartedAt, untilMs)
	if err != nil {
		s.log.Printf("bypass check: decisions: %v", err)
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
func (s *Server) bashSummaries(ctx context.Context, sessionID string, sinceMs, untilMs int64) []string {
	out, err := exec.CommandContext(ctx, "cc-transcript", "slice",
		"--session", sessionID, "--since", rfc3339Ms(sinceMs), "--until", rfc3339Ms(untilMs)).Output()
	if err != nil {
		s.warnNoSlice(err.Error())
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
			s.warnNoSlice(fmt.Sprintf("unexpected slice line %q", line))
			return nil
		}
		if row.ToolName == "Bash" {
			summaries = append(summaries, row.Summary)
		}
	}
	return summaries
}

func (s *Server) warnNoSlice(cause string) {
	s.sliceWarn.Do(func() {
		s.log.Printf("bypass check: cc-transcript slice unavailable (%s); skipping Bash attribution", cause)
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
// turn is inside the attribution window — the orphaned objects back nothing
// the differ will ever read, and the next snapshot reseeds from the repo's
// real index. Turn rows are never deleted; they back display of old versions.
func (s *Server) sweepStaleScratch(ctx context.Context, repoRoot string) {
	cutoff := time.Now().Add(-attributableWindow).UnixMilli()
	turns, err := s.store.ListAttributableTurns(ctx, repoRoot, cutoff)
	if err != nil || len(turns) > 0 {
		return
	}
	dir := paths.RepoTurnsDir(repoRoot)
	_ = os.RemoveAll(filepath.Join(dir, "objects"))
	_ = os.Remove(filepath.Join(dir, "index"))
}

// attributeVersion tags a fresh version's added lines with the turns that
// wrote them. Strictly non-fatal: any failure logs and leaves the version
// unattributed. The caller holds the repo lock, so the tree snapshotted here
// is the one the version's patch captured.
func (s *Server) attributeVersion(ctx context.Context, repoRoot string, versionID int64, patchText string) {
	scratchDir, err := paths.EnsureRepoTurnsDir(repoRoot)
	if err != nil {
		s.log.Printf("attribution: %v", err)
		return
	}
	treeNow, err := vcs.SnapshotTree(ctx, repoRoot, scratchDir)
	if err != nil {
		s.log.Printf("attribution: snapshot tree: %v", err)
		return
	}
	cutoff := time.Now().Add(-attributableWindow).UnixMilli()
	turns, err := s.store.ListAttributableTurns(ctx, repoRoot, cutoff)
	if err != nil {
		s.log.Printf("attribution: %v", err)
		return
	}
	chain, tagged := snapshotChain(turns, treeNow)
	if !tagged {
		return
	}
	byFile, err := attrib.Compute(ctx, vcs.NewTreeDiffer(repoRoot, scratchDir, treeNow.Backend), chain, patchText)
	if err != nil {
		s.log.Printf("attribution: compute: %v", err)
		return
	}
	if len(byFile) == 0 {
		return
	}
	if err := s.store.PutAttributions(ctx, versionID, byFile); err != nil {
		s.log.Printf("attribution: %v", err)
	}
}

// snapshotChain orders a repo's turns into a contiguous tree-transition chain
// ending at treeNow. Untagged gap links absorb manual edits between turns and
// the edits of interrupted turns (which have no closing snapshot); a
// still-open turn is virtually closed at treeNow without touching the DB.
// tagged is false when no turn link made the chain.
func snapshotChain(turns []store.Turn, treeNow vcs.TreeRef) (chain []attrib.Link, tagged bool) {
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
