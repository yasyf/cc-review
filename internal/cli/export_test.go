package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-interact/vcs"

	"github.com/yasyf/cc-review/internal/paths"
	"github.com/yasyf/cc-review/internal/store"
)

const exportSession = "22222222-2222-2222-2222-222222222222"

func seedActivityStore(t *testing.T) (*store.Store, vcs.Turn, vcs.Turn, string) {
	t.Helper()
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())
	if err := paths.EnsureStateDir(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.App().DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ts := vcs.NewTurnStore(st.DB())
	closed, err := ts.CreateTurn(ctx, vcs.Turn{
		RepoRoot: "/repo", Backend: "git", SessionID: exportSession, ClaudePID: 100,
		PromptExcerpt: "add parser", TreeStart: "aaa111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.CloseTurn(ctx, closed.ID, "bbb222", "closed"); err != nil {
		t.Fatal(err)
	}
	open, err := ts.CreateTurn(ctx, vcs.Turn{
		RepoRoot: "/repo", Backend: "git", SessionID: exportSession, ClaudePID: 100,
		PromptExcerpt: "fix tests", TreeStart: "bbb222",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.CreateTurn(ctx, vcs.Turn{
		RepoRoot: "/repo", Backend: "git", SessionID: "other-session", ClaudePID: 100, TreeStart: "zzz",
	}); err != nil {
		t.Fatal(err)
	}

	const branch = "main"
	ss := ccstore.NewSubjectStore(st.DB())
	sub, err := ss.Create(ctx, store.NewSlugHash(), store.ReviewSlug(branch, store.NewSlugHash()), exportSession, "/repo", 100, "open")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetReviewMeta(ctx, sub.ID, "base0", branch); err != nil {
		t.Fatal(err)
	}
	version, err := st.CreateVersion(ctx, sub.ID, branch, "HEAD", filepath.Join(t.TempDir(), "p.patch"), "[]", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAttributions(ctx, version.ID, map[string][]store.AttributionRange{
		"src/app.py": {{Start: 1, End: 4, TurnID: closed.ID}, {Start: 9, End: 9}},
	}); err != nil {
		t.Fatal(err)
	}
	return st, closed, open, sub.ID
}

func TestWriteActivityDocument(t *testing.T) {
	st, closed, open, reviewID := seedActivityStore(t)

	var buf bytes.Buffer
	if err := writeActivity(context.Background(), &buf, st, exportSession); err != nil {
		t.Fatalf("write activity: %v", err)
	}

	var doc activityExport
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	want := activityExport{
		Schema:    "cc-review.activity/1",
		SessionID: exportSession,
		Turns: []activityTurn{
			{
				TurnID: closed.ID, RepoRoot: "/repo", StartedAtMs: closed.StartedAt, EndedAtMs: doc.Turns[0].EndedAtMs,
				TreeStart: "aaa111", TreeEnd: "bbb222", Status: "closed",
			},
			{
				TurnID: open.ID, RepoRoot: "/repo", StartedAtMs: open.StartedAt, EndedAtMs: 0,
				TreeStart: "bbb222", TreeEnd: "", Status: "open",
			},
		},
		Attributions: []activityAttributed{
			{ReviewID: reviewID, Version: 1, FilePath: "src/app.py", Ranges: []activityRange{
				{Start: 1, End: 4, TurnID: &closed.ID},
				{Start: 9, End: 9, TurnID: nil},
			}},
		},
	}
	if doc.Turns[0].EndedAtMs == 0 {
		t.Fatal("closed turn exported with ended_at_ms 0")
	}
	if !reflect.DeepEqual(doc, want) {
		t.Fatalf("export = %+v, want %+v", doc, want)
	}

	var raw struct {
		Attributions []struct {
			Ranges []map[string]json.RawMessage `json:"ranges"`
		} `json:"attributions"`
	}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	turnID, ok := raw.Attributions[0].Ranges[1]["turn_id"]
	if !ok || string(turnID) != "null" {
		t.Fatalf("unattributed range turn_id = %s present=%v, want explicit null", turnID, ok)
	}
}

func TestWriteActivityEmptySessionSerializesArrays(t *testing.T) {
	st, _, _, _ := seedActivityStore(t)

	var buf bytes.Buffer
	if err := writeActivity(context.Background(), &buf, st, "33333333-3333-3333-3333-333333333333"); err != nil {
		t.Fatalf("write activity: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"turns":[]`)) {
		t.Fatalf(`export lacks "turns":[]: %s`, buf.Bytes())
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"attributions":[]`)) {
		t.Fatalf(`export lacks "attributions":[]: %s`, buf.Bytes())
	}
}

func TestExportActivityCommand(t *testing.T) {
	st, _, _, _ := seedActivityStore(t)
	_ = st.Close() // the command opens its own connection

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"export", "activity", "--session", exportSession})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var doc activityExport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode stdout: %v\n%s", err, out.Bytes())
	}
	if doc.Schema != "cc-review.activity/1" || doc.SessionID != exportSession || len(doc.Turns) != 2 {
		t.Fatalf("doc = %+v, want schema cc-review.activity/1 with 2 turns", doc)
	}
}

func TestExportActivityRequiresSession(t *testing.T) {
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"export", "activity"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error without --session")
	}
}
