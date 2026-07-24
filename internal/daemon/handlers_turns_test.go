package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
)

func mustTurnOK(t *testing.T, resp Response, op string) {
	t.Helper()
	if !resp.OK {
		t.Fatalf("%s: %s", op, resp.Error)
	}
}

func TestTurnLifecycle(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "add a feature"}), "turn-start")
	open, ok, err := s.turns.LatestOpenTurn(ctx, root, 100)
	if err != nil || !ok {
		t.Fatalf("open turn: ok=%v err=%v", ok, err)
	}
	if open.SessionID != "s1" || open.PromptExcerpt != "add a feature" {
		t.Fatalf("turn = %+v", open)
	}
	if open.Backend != "git" || open.TreeStart == "" || open.TreeEnd != "" {
		t.Fatalf("backend=%q trees=%q..%q, want an open git bracket", open.Backend, open.TreeStart, open.TreeEnd)
	}

	writeFile(t, repo, "feat.go", "package p\nvar Feat int\n")
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end")

	turns, err := s.turns.ListAttributableTurns(ctx, root, 0)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns = %d (err %v), want 1", len(turns), err)
	}
	closed := turns[0]
	if closed.Status != "closed" || closed.EndedAt == 0 {
		t.Fatalf("status=%q ended_at=%d, want closed with a timestamp", closed.Status, closed.EndedAt)
	}
	if closed.TreeEnd == "" || closed.TreeEnd == closed.TreeStart {
		t.Fatalf("trees = %q..%q, want a moved closing tree", closed.TreeStart, closed.TreeEnd)
	}

	// A second end with nothing open is a silent no-op.
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end again")
}

func TestTurnOpsOutsideRepoAreNoops(t *testing.T) {
	ctx := context.Background()
	s, _ := testServer(t)
	outside := t.TempDir()

	// Outside a repo the scope falls back to the cwd as given: turn-start fails
	// where its precondition lives (the tree snapshot), turn-end finds no open
	// turn and no-ops. Either way the hook stays silent.
	resp := s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: outside, Prompt: "p"})
	if resp.OK || !strings.Contains(resp.Error, "not inside a git or jj repository") {
		t.Fatalf("turn-start outside a repo: ok=%v err=%q, want the snapshot error", resp.OK, resp.Error)
	}
	if resp := s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: outside}); !resp.OK {
		t.Fatalf("turn-end outside a repo = %+v, want a silent ok no-op", resp)
	}
	turns, err := s.turns.ListAttributableTurns(ctx, outside, 0)
	if err != nil || len(turns) != 0 {
		t.Fatalf("turns for the non-repo scope = %d (err %v), want none recorded", len(turns), err)
	}
}

func TestTurnStartInterruptsDanglingTurn(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "one"}), "first turn-start")
	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "two"}), "second turn-start")

	turns, err := s.turns.ListAttributableTurns(ctx, root, 0)
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns = %d (err %v), want 2", len(turns), err)
	}
	first, second := turns[0], turns[1]
	if first.Status != "interrupted" || first.TreeEnd != "" || first.EndedAt == 0 {
		t.Fatalf("first = %+v, want interrupted with no closing tree", first)
	}
	if second.Status != "open" || second.PromptExcerpt != "two" {
		t.Fatalf("second = %+v, want open", second)
	}
}

func TestTurnStartSweepsStaleScratch(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)

	stale, err := s.turns.CreateTurn(ctx, vcs.Turn{RepoRoot: root, Backend: "git", ClaudePID: 50, TreeStart: "deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	backdate := time.Now().Add(-attributableWindow - 24*time.Hour).UnixMilli()
	if _, err := s.store.DB().ExecContext(ctx, `UPDATE turns SET started_at=? WHERE id=?`, backdate, stale.ID); err != nil {
		t.Fatal(err)
	}

	scratchDir, err := paths.App().EnsureRepoTurnsDir(root)
	if err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(scratchDir, "objects")
	if err := os.MkdirAll(objects, 0o750); err != nil {
		t.Fatal(err)
	}
	dummy := filepath.Join(objects, "stale-object")
	if err := os.WriteFile(dummy, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchDir, "index"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "p"}), "turn-start")

	if _, err := os.Stat(dummy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale scratch object survived the sweep: %v", err)
	}
	open, ok, err := s.turns.LatestOpenTurn(ctx, root, 100)
	if err != nil || !ok || open.TreeStart == "" {
		t.Fatalf("snapshot after sweep: ok=%v err=%v turn=%+v", ok, err, open)
	}
	// Turn rows are never deleted: the swept repo's old row still backs the
	// display of old versions.
	rows, err := s.turns.ListTurnsByIDs(ctx, []int64{stale.ID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("stale turn row = %d (err %v), want 1", len(rows), err)
	}
}

// stubSliceBinary fronts PATH with a fake cc-transcript whose slice verb runs
// script (a /bin/sh body writing slice JSONL to stdout).
func stubSliceBinary(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cc-transcript"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil { //nolint:gosec // G306: test stub must be executable (0o755) so the code under test can exec it as cc-transcript.
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func bypassRow(t *testing.T, s *Server, session string) *decisions.Decision {
	t.Helper()
	rows, err := s.decisions.ForTurn(session, 0, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rows {
		if d.Kind == "bypass-detected" {
			return &d
		}
	}
	return nil
}

func TestTurnEndFlagsUnattributedChanges(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	stubSliceBinary(t, `echo '{"schema":"cc-transcript.slice/1","event_uuid":"u1","tool_use_id":"toolu_1","ts_ms":1,"tool_name":"Bash","tool_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","file_path":null,"summary":"$ go test ./..."}'`)
	if _, err := s.createReview(ctx, "s1", 100, root, "main", "base0"); err != nil {
		t.Fatal(err)
	}

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "p"}), "turn-start")
	turn, ok, err := s.turns.LatestOpenTurn(ctx, root, 100)
	if err != nil || !ok {
		t.Fatalf("open turn: ok=%v err=%v", ok, err)
	}
	blocked := s.handleGuardEdit(ctx, Request{
		Session: "s1", ClaudePID: 100, Cwd: repo, ToolName: "Edit",
		ToolInput: json.RawMessage(`{"file_path":"` + filepath.Join(root, "sneaky.go") + `","old_string":"a","new_string":"b"}`),
	})
	if blocked.Allow {
		t.Fatal("guard-edit must block while the review is open")
	}
	writeFile(t, repo, "sneaky.go", "package p\nvar X int\n") // the sed-equivalent the gate never saw run
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end")

	row := bypassRow(t, s, "s1")
	if row == nil {
		t.Fatal("no bypass-detected row for an unattributed tree change")
	}
	if row.Source != "cc-review" || row.Event != "Stop" || row.Action != "note" || row.Message == "" {
		t.Fatalf("bypass row = %+v, want a cc-review Stop note with a message", row)
	}
	var detail struct {
		ChangedFiles    []string `json:"changed_files"`
		AttributedFiles []string `json:"attributed_files"`
		TurnID          int64    `json:"turn_id"`
	}
	if err := json.Unmarshal([]byte(row.DetailJSON), &detail); err != nil {
		t.Fatalf("detail %q: %v", row.DetailJSON, err)
	}
	// The gate-blocked edit never ran, so its path must not count as attributed.
	if !reflect.DeepEqual(detail.ChangedFiles, []string{"sneaky.go"}) || len(detail.AttributedFiles) != 0 || detail.TurnID != turn.ID {
		t.Fatalf("detail = %+v, want changed=[sneaky.go] attributed=[] turn_id=%d", detail, turn.ID)
	}
}

func TestTurnEndFullyAttributedWritesNoBypassRow(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)
	stubSliceBinary(t, `echo '{"schema":"cc-transcript.slice/1","event_uuid":"u1","tool_use_id":"toolu_1","ts_ms":1,"tool_name":"Bash","tool_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","file_path":null,"summary":"$ sed -i s/a/b/ sneaky.go"}'`)
	if _, err := s.createReview(ctx, "s1", 100, root, "main", "base0"); err != nil {
		t.Fatal(err)
	}

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "p"}), "turn-start")
	writeFile(t, repo, "sneaky.go", "package p\nvar X int\n")
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end")

	if row := bypassRow(t, s, "s1"); row != nil {
		t.Fatalf("bypass row = %+v, want none when a sliced Bash call names every changed file", row)
	}
}

func TestHandleStartAttributesTurnLines(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "turn one"}), "turn-start 1")
	writeFile(t, repo, "feat.go", "package p\nvar Turn1 int\n")
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end 1")

	// A manual edit between turns: the chain's untagged gap link absorbs it.
	writeFile(t, repo, "feat.go", "package p\nvar Turn1 int\nvar Manual int\n")

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "turn two"}), "turn-start 2")
	writeFile(t, repo, "feat.go", "package p\nvar Turn1 int\nvar Manual int\nvar Turn2 int\n")
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end 2")

	turns, err := s.turns.ListAttributableTurns(ctx, root, 0)
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns = %d (err %v), want 2", len(turns), err)
	}

	started := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	sections := s.latestSections(ctx, t, started.ReviewID)
	byFile, err := s.store.ListAttributionsBySection(ctx, sections[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.AttributionRange{
		{Start: 1, End: 2, TurnID: turns[0].ID},
		{Start: 3, End: 3},
		{Start: 4, End: 4, TurnID: turns[1].ID},
	}
	if got := byFile["feat.go"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("feat.go ranges = %+v, want %+v", got, want)
	}
}
