package daemon

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/yasyf/cc-review/internal/attrib"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
	"github.com/yasyf/cc-review/internal/vcs"
)

// attributableWindow bounds how far back turns participate in attribution; once
// a repo has no turn inside it, the snapshot scratch state is swept.
const attributableWindow = 14 * 24 * time.Hour

// promptExcerptMax caps the stored prompt excerpt, in runes.
const promptExcerptMax = 1000

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
	offset := int64(-1)
	if req.TranscriptPath != "" {
		if info, err := os.Stat(req.TranscriptPath); err == nil {
			offset = info.Size()
		}
	}
	if _, err := s.store.CreateTurn(ctx, store.Turn{
		RepoRoot: repoRoot, Backend: tree.Backend, SessionID: req.Session, ClaudePID: req.ClaudePID,
		PromptExcerpt: promptExcerpt(req.Prompt), TranscriptPath: req.TranscriptPath, TranscriptOffset: offset,
		TreeStart: tree.OID,
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
	return Response{OK: true}
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
