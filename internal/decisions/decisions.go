// Package decisions writes and reads the cc-family decision ledger: the
// decisions SQLite table at ~/.cc-transcript/decisions.db, shared with
// cc-transcript's Python DecisionLog, which writes the same file concurrently
// (hence WAL + busy_timeout on open). The DDL is vendored byte-identical from
// the Python source of truth and executed verbatim; rows are append-only and
// never dropped.
package decisions

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed decisions.sql
var ddl string

const decisionCols = `ts_ms, session_id, source, kind, source_file, event, action, tool_name, tool_digest, event_uuid, message, detail_json`

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

// Open opens (creating parents if needed) the ledger at path and applies the
// vendored DDL verbatim. WAL plus a 2000ms busy timeout because the Python
// DecisionLog writes the same file concurrently; requires a local disk.
func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create ledger dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(2000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open decisions ledger: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply decisions ddl: %w", err)
	}
	return &Log{db: db}, nil
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
	defer rows.Close()
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
