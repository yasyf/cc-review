package decisions

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func openTest(t *testing.T, path string) *Log {
	t.Helper()
	log, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = log.Close() })
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

func TestOpenCreatesAndReopensExactSchemaV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "decisions.db")
	log := openTest(t, path)

	var component, storedDDL, storedObjects string
	var version int
	if err := log.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
	if err := log.db.QueryRow(
		`SELECT component, schema_version, ddl_fingerprint, object_fingerprint FROM cc_review_decisions_schema_v1 WHERE id=1`).
		Scan(&component, &version, &storedDDL, &storedObjects); err != nil {
		t.Fatalf("read schema marker: %v", err)
	}
	if component != schemaComponent || version != schemaVersion || storedDDL != expectedDDLFingerprint || storedObjects != expectedObjectFingerprint {
		t.Fatalf("schema marker = (%q, %d, %q, %q)", component, version, storedDDL, storedObjects)
	}
	gotObjects, err := fingerprintObjects(t.Context(), log.db)
	if err != nil {
		t.Fatalf("fingerprint objects: %v", err)
	}
	if gotObjects != expectedObjectFingerprint {
		t.Fatalf("object fingerprint = %q, want %q", gotObjects, expectedObjectFingerprint)
	}
	if got := ddlFingerprint(); got != expectedDDLFingerprint {
		t.Fatalf("DDL fingerprint = %q, want %q", got, expectedDDLFingerprint)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close exact v1: %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen exact v1: %v", err)
	}
	_ = reopened.Close()
}

type schemaObject struct {
	ObjectType string
	Name       string
	Table      string
	SQL        string
}

type schemaSnapshot struct {
	Version int
	Objects []schemaObject
	Marker  []any
}

func readSchemaSnapshot(t *testing.T, path string) schemaSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	defer func() { _ = db.Close() }()
	var snapshot schemaSnapshot
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&snapshot.Version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	rows, err := db.Query(`SELECT type, name, tbl_name, sql FROM sqlite_schema ORDER BY type, name`)
	if err != nil {
		t.Fatalf("list schema objects: %v", err)
	}
	for rows.Next() {
		var object schemaObject
		var statement sql.NullString
		if err := rows.Scan(&object.ObjectType, &object.Name, &object.Table, &statement); err != nil {
			t.Fatalf("scan schema object: %v", err)
		}
		object.SQL = statement.String
		snapshot.Objects = append(snapshot.Objects, object)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close schema rows: %v", err)
	}
	for _, object := range snapshot.Objects {
		if object.Name != "cc_review_decisions_schema_v1" {
			continue
		}
		var component, ddlFingerprint, objectFingerprint string
		var id, version int
		err := db.QueryRow(
			`SELECT id, component, schema_version, ddl_fingerprint, object_fingerprint FROM cc_review_decisions_schema_v1`).
			Scan(&id, &component, &version, &ddlFingerprint, &objectFingerprint)
		if err == nil {
			snapshot.Marker = []any{id, component, version, ddlFingerprint, objectFingerprint}
		} else if err != sql.ErrNoRows {
			snapshot.Marker = []any{err.Error()}
		}
		break
	}
	return snapshot
}

func mutateDatabase(t *testing.T, path, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	if _, err := db.Exec(statement); err != nil {
		_ = db.Close()
		t.Fatalf("mutate database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}
}

func createExactDatabase(t *testing.T, path string) {
	t.Helper()
	log, err := Open(path)
	if err != nil {
		t.Fatalf("create exact database: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close exact database: %v", err)
	}
}

func TestOpenCreatesOnlyFromEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create empty database file: %v", err)
	}
	log, err := Open(path)
	if err != nil {
		t.Fatalf("Open(empty): %v", err)
	}
	_ = log.Close()
	if got := readSchemaSnapshot(t, path); len(got.Objects) == 0 {
		t.Fatal("empty database was not initialized")
	}
}

func TestOpenRejectsNonExactShapesWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*testing.T, string)
		needle string
	}{
		{
			name: "old unversioned decisions table",
			setup: func(t *testing.T, path string) {
				mutateDatabase(t, path, `CREATE TABLE decisions(id INTEGER PRIMARY KEY)`)
			},
			needle: "schema version",
		},
		{
			name: "partial marker only",
			setup: func(t *testing.T, path string) {
				mutateDatabase(t, path, `CREATE TABLE cc_review_decisions_schema_v1(id INTEGER PRIMARY KEY)`)
			},
			needle: "schema version",
		},
		{
			name: "missing index",
			setup: func(t *testing.T, path string) {
				createExactDatabase(t, path)
				mutateDatabase(t, path, `DROP INDEX idx_decisions_tool_digest`)
			},
			needle: "object fingerprint",
		},
		{
			name: "extra table",
			setup: func(t *testing.T, path string) {
				createExactDatabase(t, path)
				mutateDatabase(t, path, `CREATE TABLE foreign_state(id TEXT PRIMARY KEY)`)
			},
			needle: "object fingerprint",
		},
		{
			name: "foreign DDL fingerprint",
			setup: func(t *testing.T, path string) {
				createExactDatabase(t, path)
				mutateDatabase(t, path, `UPDATE cc_review_decisions_schema_v1 SET ddl_fingerprint='foreign' WHERE id=1`)
			},
			needle: "DDL fingerprint",
		},
		{
			name: "foreign schema version",
			setup: func(t *testing.T, path string) {
				createExactDatabase(t, path)
				mutateDatabase(t, path, `PRAGMA user_version = 2`)
			},
			needle: "schema version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "decisions.db")
			tc.setup(t, path)
			before := readSchemaSnapshot(t, path)
			log, err := Open(path)
			if log != nil {
				_ = log.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.needle) {
				t.Fatalf("Open() = %v, want error containing %q", err, tc.needle)
			}
			after := readSchemaSnapshot(t, path)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected open mutated database:\n before: %#v\n  after: %#v", before, after)
			}
		})
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
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(string(vendored)); err != nil {
		t.Fatalf("vendored DDL failed to apply: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO decisions (` + decisionCols + `) VALUES (1, 's', 'cc-review', 'k', '', 'PreToolUse', 'allow', NULL, NULL, NULL, NULL, '{}')`); err != nil {
		t.Fatalf("insert into fresh schema: %v", err)
	}
}
