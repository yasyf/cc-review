package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yasyf/cc-review/internal/decisions"
	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/session"
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
	open, ok, err := s.store.LatestOpenTurn(ctx, root, 100)
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

	turns, err := s.store.ListAttributableTurns(ctx, root, 0)
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

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: outside, Prompt: "p"}), "turn-start outside a repo")
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: outside}), "turn-end outside a repo")
}

func TestTurnStartInterruptsDanglingTurn(t *testing.T) {
	ctx := context.Background()
	s, repo := testServer(t)
	root := repoRoot(t, repo)

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "one"}), "first turn-start")
	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "two"}), "second turn-start")

	turns, err := s.store.ListAttributableTurns(ctx, root, 0)
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
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, "base.go", "package p\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "init")
	s := &Server{
		store:     st,
		bus:       NewBus(),
		activity:  NewActivity(),
		alive:     func(int) bool { return false },
		log:       log.New(io.Discard, "", 0),
		repoLocks: make(map[string]*sync.Mutex),
	}
	s.resolver = session.Resolver{Store: st, Held: s.held}
	root := repoRoot(t, repo)

	stale, err := st.CreateTurn(ctx, store.Turn{RepoRoot: root, Backend: "git", ClaudePID: 50, TreeStart: "deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	backdate := time.Now().Add(-attributableWindow - 24*time.Hour).UnixMilli()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE turns SET started_at=? WHERE id=?`, backdate, stale.ID); err != nil {
		t.Fatal(err)
	}
	db.Close()

	scratchDir, err := paths.EnsureRepoTurnsDir(root)
	if err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(scratchDir, "objects")
	if err := os.MkdirAll(objects, 0o755); err != nil {
		t.Fatal(err)
	}
	dummy := filepath.Join(objects, "stale-object")
	if err := os.WriteFile(dummy, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratchDir, "index"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "p"}), "turn-start")

	if _, err := os.Stat(dummy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale scratch object survived the sweep: %v", err)
	}
	open, ok, err := s.store.LatestOpenTurn(ctx, root, 100)
	if err != nil || !ok || open.TreeStart == "" {
		t.Fatalf("snapshot after sweep: ok=%v err=%v turn=%+v", ok, err, open)
	}
	// Turn rows are never deleted: the swept repo's old row still backs the
	// display of old versions.
	rows, err := st.ListTurnsByIDs(ctx, []int64{stale.ID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("stale turn row = %d (err %v), want 1", len(rows), err)
	}
}

// stubSliceBinary fronts PATH with a fake cc-transcript whose slice verb runs
// script (a /bin/sh body writing slice JSONL to stdout).
func stubSliceBinary(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cc-transcript"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
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
	if _, err := s.store.CreateReview(ctx, "s1", 100, root, "main", "base0"); err != nil {
		t.Fatal(err)
	}

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "p"}), "turn-start")
	turn, ok, err := s.store.LatestOpenTurn(ctx, root, 100)
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
	if _, err := s.store.CreateReview(ctx, "s1", 100, root, "main", "base0"); err != nil {
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
	s.alive = aliveSet(100)
	root := repoRoot(t, repo)

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "turn one"}), "turn-start 1")
	writeFile(t, repo, "feat.go", "package p\nvar Turn1 int\n")
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end 1")

	// A manual edit between turns: the chain's untagged gap link absorbs it.
	writeFile(t, repo, "feat.go", "package p\nvar Turn1 int\nvar Manual int\n")

	mustTurnOK(t, s.handleTurnStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo, Prompt: "turn two"}), "turn-start 2")
	writeFile(t, repo, "feat.go", "package p\nvar Turn1 int\nvar Manual int\nvar Turn2 int\n")
	mustTurnOK(t, s.handleTurnEnd(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo}), "turn-end 2")

	turns, err := s.store.ListAttributableTurns(ctx, root, 0)
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns = %d (err %v), want 2", len(turns), err)
	}

	started := s.handleStart(ctx, Request{Session: "s1", ClaudePID: 100, Cwd: repo})
	if !started.OK {
		t.Fatalf("start: %s", started.Error)
	}
	v, ok, err := s.store.LatestVersion(ctx, started.ReviewID)
	if err != nil || !ok {
		t.Fatalf("latest version: ok=%v err=%v", ok, err)
	}
	byFile, err := s.store.ListAttributionsByVersion(ctx, v.ID)
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
