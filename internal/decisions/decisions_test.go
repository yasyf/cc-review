package decisions

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T, path string) *Log {
	t.Helper()
	log, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { log.Close() })
	return log
}

func gateDecision(tsMs int64) Decision {
	return Decision{
		TsMs:       tsMs,
		SessionID:  "11111111-2222-3333-4444-555555555555",
		Source:     "cc-review",
		Kind:       "guard-edit",
		SourceFile: "",
		Event:      "PreToolUse",
		Action:     "block",
		ToolName:   "Edit",
		ToolDigest: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Message:    "locked review",
		DetailJSON: `{"review_id":"abc"}`,
	}
}

func TestOpenAppliesSchemaAndCreatesParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "decisions.db")
	log := openTest(t, path)

	var name string
	if err := log.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='decisions'`).Scan(&name); err != nil {
		t.Fatalf("decisions table missing: %v", err)
	}
	for _, idx := range []string{"idx_decisions_session_ts", "idx_decisions_tool_digest", "idx_decisions_source_file"} {
		if err := log.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name); err != nil {
			t.Fatalf("index %s missing: %v", idx, err)
		}
	}
}

func TestAppendIdempotency(t *testing.T) {
	t.Run("digest rows dedupe on the UNIQUE tuple", func(t *testing.T) {
		log := openTest(t, filepath.Join(t.TempDir(), "decisions.db"))
		d := gateDecision(1000)
		if err := log.Append(d); err != nil {
			t.Fatalf("first append: %v", err)
		}
		if err := log.Append(d); err != nil {
			t.Fatalf("second append: %v", err)
		}
		got, err := log.ForTurn(d.SessionID, 0, 2000)
		if err != nil {
			t.Fatalf("ForTurn: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d rows, want 1", len(got))
		}
		if got[0] != d {
			t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got[0], d)
		}
	})

	t.Run("digestless rows are distinct under NULL", func(t *testing.T) {
		log := openTest(t, filepath.Join(t.TempDir(), "decisions.db"))
		d := gateDecision(1000)
		d.ToolName, d.ToolDigest, d.Message = "", "", ""
		for range 2 {
			if err := log.Append(d); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
		got, err := log.ForTurn(d.SessionID, 0, 2000)
		if err != nil {
			t.Fatalf("ForTurn: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d rows, want 2 (NULL digests never collide)", len(got))
		}
	})
}

func TestNullRoundTrip(t *testing.T) {
	log := openTest(t, filepath.Join(t.TempDir(), "decisions.db"))
	d := Decision{
		TsMs:      1000,
		SessionID: "11111111-2222-3333-4444-555555555555",
		Source:    "cc-review",
		Kind:      "bypass-detected",
		Event:     "Stop",
		Action:    "note",
	}
	if err := log.Append(d); err != nil {
		t.Fatalf("append: %v", err)
	}

	var n int
	if err := log.db.QueryRow(
		`SELECT count(*) FROM decisions
		 WHERE tool_name IS NULL AND tool_digest IS NULL AND event_uuid IS NULL AND message IS NULL
		   AND source_file = '' AND detail_json = '{}'`).Scan(&n); err != nil {
		t.Fatalf("count nulls: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d NULL-optional rows, want 1 (empty strings must store as NULL)", n)
	}

	got, err := log.ForTurn(d.SessionID, 0, 2000)
	if err != nil {
		t.Fatalf("ForTurn: %v", err)
	}
	want := d
	want.DetailJSON = "{}"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestForTurnWindow(t *testing.T) {
	log := openTest(t, filepath.Join(t.TempDir(), "decisions.db"))
	session := "11111111-2222-3333-4444-555555555555"
	for _, ts := range []int64{300, 100, 200} {
		if err := log.Append(gateDecision(ts)); err != nil {
			t.Fatalf("append ts=%d: %v", ts, err)
		}
	}
	other := gateDecision(200)
	other.SessionID = "99999999-8888-7777-6666-555555555555"
	if err := log.Append(other); err != nil {
		t.Fatalf("append other session: %v", err)
	}

	for _, tc := range []struct {
		name             string
		sinceMs, untilMs int64
		want             []int64
	}{
		{"inclusive bounds, ordered by ts", 100, 300, []int64{100, 200, 300}},
		{"interior window", 150, 250, []int64{200}},
		{"single millisecond", 100, 100, []int64{100}},
		{"empty window", 301, 400, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := log.ForTurn(session, tc.sinceMs, tc.untilMs)
			if err != nil {
				t.Fatalf("ForTurn: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d rows, want %d", len(got), len(tc.want))
			}
			for i, ts := range tc.want {
				if got[i].TsMs != ts {
					t.Fatalf("row %d: got ts %d, want %d", i, got[i].TsMs, ts)
				}
				if got[i].SessionID != session {
					t.Fatalf("row %d: got session %q, want %q", i, got[i].SessionID, session)
				}
			}
		})
	}
}

func TestInterleavedWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.db")
	a := openTest(t, path)
	b := openTest(t, path)
	session := gateDecision(0).SessionID

	for ts := int64(1); ts <= 6; ts++ {
		writer := a
		if ts%2 == 0 {
			writer = b
		}
		if err := writer.Append(gateDecision(ts)); err != nil {
			t.Fatalf("append ts=%d: %v", ts, err)
		}
	}

	for name, log := range map[string]*Log{"a": a, "b": b} {
		got, err := log.ForTurn(session, 1, 6)
		if err != nil {
			t.Fatalf("ForTurn via %s: %v", name, err)
		}
		if len(got) != 6 {
			t.Fatalf("via %s: got %d rows, want 6", name, len(got))
		}
		for i, d := range got {
			if d.TsMs != int64(i+1) {
				t.Fatalf("via %s: row %d has ts %d, want %d", name, i, d.TsMs, i+1)
			}
		}
	}
}

func TestVendoredDDLMatchesEmbedAndApplies(t *testing.T) {
	vendored, err := os.ReadFile("decisions.sql")
	if err != nil {
		t.Fatalf("read vendored DDL: %v", err)
	}
	if string(vendored) != ddl {
		t.Fatalf("embedded DDL diverges from decisions.sql")
	}

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("open fresh db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(string(vendored)); err != nil {
		t.Fatalf("vendored DDL failed to apply: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO decisions (` + decisionCols + `) VALUES (1, 's', 'cc-review', 'k', '', 'PreToolUse', 'allow', NULL, NULL, NULL, NULL, '{}')`); err != nil {
		t.Fatalf("insert into fresh schema: %v", err)
	}
}
