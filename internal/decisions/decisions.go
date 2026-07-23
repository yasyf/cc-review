// Package decisions writes and reads the exact v1 cc-family decision ledger.
package decisions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	// modernc.org/sqlite registers the pure-Go "sqlite" database/sql driver
	// used to open the decisions ledger.
	_ "modernc.org/sqlite"
)

//go:embed decisions.sql
var ddl string

const (
	schemaComponent           = "cc-review-decisions-v1"
	schemaVersion             = 1
	expectedDDLFingerprint    = "6ae938038f3420cdd4a00189b678fb399d60bb7647d009acb0fa9cc4a653040f"
	expectedObjectFingerprint = "a993521f1ae402d85545d9cd841b58c7e9ba755babba32c7d59cc3a97ee17af9"
	decisionCols              = `ts_ms, session_id, source, kind, source_file, event, action, tool_name, tool_digest, event_uuid, message, detail_json`
)

// Decision is one row of the decision ledger. TsMs is integer milliseconds
// and part of the UNIQUE key (session_id, ts_ms, source, kind, tool_digest),
// so re-running a writer is exactly idempotent for digest-carrying rows.
// Empty ToolName/ToolDigest/EventUUID/Message are stored as NULL, never
// the empty string.
type Decision struct {
	TsMs       int64
	SessionID  string // the Claude session UUID
	Source     string // the writing system, e.g. cc-review
	Kind       string // the writer's decision taxonomy, e.g. guard-edit
	SourceFile string // hook file provenance; '' when the writer has none
	Event      string // the Claude Code event, e.g. PreToolUse
	Action     string // allow | block | warn | nudge | note
	ToolName   string // '' when the event is not tool-shaped
	ToolDigest string // cross-language content digest; the attribution key
	EventUUID  string // transcript entry uuid, when known
	Message    string // user-visible decision text, if any
	DetailJSON string // structured extras; '' written as {}
}

// Log is the decisions ledger on a single serialized connection.
type Log struct {
	db *sql.DB
}

// DefaultPath is the canonical ledger location, ~/.cc-transcript/decisions.db
// — cc-transcript's directory, not cc-review's, because the file is shared
// family-wide.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("resolve home dir: %v", err))
	}
	return filepath.Join(home, ".cc-transcript", "decisions.db")
}

// Open opens the exact v1 ledger, creating it only when it has no user objects.
func Open(ctx context.Context, path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create ledger dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, fmt.Errorf("open decisions ledger: %w", err)
	}
	db.SetMaxOpenConns(1)
	created, err := initializeOrVerify(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if created {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("secure decisions ledger: %w", err)
		}
	}
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&journalMode); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable decisions WAL: %w", err)
	}
	if journalMode != "wal" {
		_ = db.Close()
		return nil, fmt.Errorf("enable decisions WAL: mode %q", journalMode)
	}
	return &Log{db: db}, nil
}

func initializeOrVerify(ctx context.Context, db *sql.DB) (bool, error) {
	if got := ddlFingerprint(); got != expectedDDLFingerprint {
		return false, fmt.Errorf("decisions compiled DDL fingerprint %q, want exactly %q", got, expectedDDLFingerprint)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire decisions connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return false, fmt.Errorf("begin decisions schema transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
		}
	}()

	var objectCount int
	if err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&objectCount); err != nil {
		return false, fmt.Errorf("count decisions schema objects: %w", err)
	}
	created := objectCount == 0
	if created {
		if _, err := conn.ExecContext(ctx, ddl); err != nil {
			return false, fmt.Errorf("create decisions v1 schema: %w", err)
		}
		if _, err := conn.ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
			return false, fmt.Errorf("record decisions schema version: %w", err)
		}
		objectFingerprint, err := fingerprintObjects(ctx, conn)
		if err != nil {
			return false, err
		}
		if objectFingerprint != expectedObjectFingerprint {
			return false, fmt.Errorf("decisions object fingerprint %q, want exactly %q", objectFingerprint, expectedObjectFingerprint)
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO cc_review_decisions_schema_v1(id, component, schema_version, ddl_fingerprint, object_fingerprint) VALUES(1, ?, 1, ?, ?)`,
			schemaComponent, expectedDDLFingerprint, expectedObjectFingerprint); err != nil {
			return false, fmt.Errorf("record decisions schema identity: %w", err)
		}
	} else if err := verifySchema(ctx, conn); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return false, fmt.Errorf("commit decisions schema transaction: %w", err)
	}
	committed = true
	return created, nil
}

func verifySchema(ctx context.Context, conn *sql.Conn) error {
	var version int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read decisions schema version: %w", err)
	}
	if version != schemaVersion {
		return fmt.Errorf("decisions schema version %d, want exactly %d", version, schemaVersion)
	}
	var component, storedDDL, storedObjects string
	var storedVersion int
	if err := conn.QueryRowContext(ctx,
		`SELECT component, schema_version, ddl_fingerprint, object_fingerprint FROM cc_review_decisions_schema_v1 WHERE id=1`).
		Scan(&component, &storedVersion, &storedDDL, &storedObjects); err != nil {
		return fmt.Errorf("read decisions schema identity: %w", err)
	}
	if component != schemaComponent {
		return fmt.Errorf("decisions schema component %q, want exactly %q", component, schemaComponent)
	}
	if storedVersion != schemaVersion {
		return fmt.Errorf("decisions marker version %d, want exactly %d", storedVersion, schemaVersion)
	}
	wantDDL := expectedDDLFingerprint
	if storedDDL != wantDDL {
		return fmt.Errorf("decisions DDL fingerprint %q, want exactly %q", storedDDL, wantDDL)
	}
	if storedObjects != expectedObjectFingerprint {
		return fmt.Errorf("decisions stored object fingerprint %q, want exactly %q", storedObjects, expectedObjectFingerprint)
	}
	actualObjects, err := fingerprintObjects(ctx, conn)
	if err != nil {
		return err
	}
	if actualObjects != expectedObjectFingerprint {
		return fmt.Errorf("decisions object fingerprint %q, want exactly %q", actualObjects, expectedObjectFingerprint)
	}
	return nil
}

func ddlFingerprint() string {
	sum := sha256.Sum256([]byte("cc-review-decisions-ddl-v1\x00" + ddl))
	return hex.EncodeToString(sum[:])
}

type schemaQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func fingerprintObjects(ctx context.Context, db schemaQuerier) (string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT type, name, tbl_name, sql FROM sqlite_schema ORDER BY type, name`)
	if err != nil {
		return "", fmt.Errorf("list decisions schema objects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	_, _ = hash.Write([]byte("cc-review-decisions-objects-v1\x00"))
	for rows.Next() {
		var objectType, name, table string
		var statement sql.NullString
		if err := rows.Scan(&objectType, &name, &table, &statement); err != nil {
			return "", fmt.Errorf("scan decisions schema object: %w", err)
		}
		for _, field := range []string{objectType, name, table, statement.String} {
			_, _ = hash.Write([]byte(field))
			_, _ = hash.Write([]byte{0})
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("list decisions schema objects: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Close closes the underlying database.
func (l *Log) Close() error { return l.db.Close() }

// Append records d as a single INSERT OR IGNORE. Idempotent on the UNIQUE
// key when ToolDigest is present; SQLite treats NULL digests as distinct, so
// digestless rows rely on the writer not re-running the same millisecond.
func (l *Log) Append(d Decision) error {
	detail := d.DetailJSON
	if detail == "" {
		detail = "{}"
	}
	if _, err := l.db.Exec(
		`INSERT OR IGNORE INTO decisions (`+decisionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.TsMs, d.SessionID, d.Source, d.Kind, d.SourceFile, d.Event, d.Action,
		nullString(d.ToolName), nullString(d.ToolDigest), nullString(d.EventUUID), nullString(d.Message), detail,
	); err != nil {
		return fmt.Errorf("append decision: %w", err)
	}
	return nil
}

// ForTurn returns a session's decisions with ts_ms in [sinceMs, untilMs]
// (inclusive), ordered by timestamp — the read behind the UI turn panel and
// the bypass check.
func (l *Log) ForTurn(sessionID string, sinceMs, untilMs int64) ([]Decision, error) {
	rows, err := l.db.Query(
		`SELECT `+decisionCols+` FROM decisions WHERE session_id = ? AND ts_ms BETWEEN ? AND ? ORDER BY ts_ms, id`,
		sessionID, sinceMs, untilMs)
	if err != nil {
		return nil, fmt.Errorf("decisions for turn: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanDecision(row interface{ Scan(...any) error }) (Decision, error) {
	var (
		d                                        Decision
		toolName, toolDigest, eventUUID, message sql.NullString
	)
	if err := row.Scan(&d.TsMs, &d.SessionID, &d.Source, &d.Kind, &d.SourceFile, &d.Event, &d.Action,
		&toolName, &toolDigest, &eventUUID, &message, &d.DetailJSON); err != nil {
		return Decision{}, err
	}
	d.ToolName, d.ToolDigest, d.EventUUID, d.Message = toolName.String, toolDigest.String, eventUUID.String, message.String
	return d, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
