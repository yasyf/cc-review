// Package store is cc-review's domain state layer on top of cc-interact's
// append-only core (subjects + the per-subject event log). It adds the review
// domain tables — per-review pinned base (review_meta), diff versions, inline
// comments, the back-and-forth replies, file states, AI requests, organizations,
// and turn attributions — and their CRUD. Rows are never deleted; status flags
// carry state. Large patches live on disk (see internal/paths); the DB keeps
// only the patch path and a files summary.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-interact/vcs"
)

// ErrNotFound is returned when a lookup by id finds no row.
var ErrNotFound = ccstore.ErrNotFound

// Store holds the review domain tables on a borrowed single-writer connection.
// The connection (and the core subjects/events schema) belong to cc-interact's
// store; a Store opened via Open owns its Close, one built via New does not.
type Store struct {
	cc *ccstore.Store // nil when wrapping a borrowed connection
	db *sql.DB
}

// schemaV1 is the review domain schema layered on cc-interact's core
// subjects/events tables. review_meta pins each review's diff base and creation
// branch (the per-version base_ref still lives on review_versions). Foreign keys
// point at the core subjects table the core schema created first.
const schemaV1 = `
CREATE TABLE review_meta (
  subject_id TEXT PRIMARY KEY REFERENCES subjects(id),
  base_ref   TEXT NOT NULL DEFAULT '',
  branch     TEXT NOT NULL DEFAULT ''
);
CREATE TABLE review_versions (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  review_id      TEXT NOT NULL REFERENCES subjects(id),
  version_number INTEGER NOT NULL,
  branch         TEXT NOT NULL DEFAULT '',
  base_ref       TEXT NOT NULL DEFAULT '',
  patch_path     TEXT NOT NULL,
  files_json     TEXT NOT NULL DEFAULT '[]',
  session_id     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  UNIQUE(review_id, version_number)
);
CREATE TABLE comments (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  version_id   INTEGER NOT NULL REFERENCES review_versions(id),
  file_path    TEXT NOT NULL,
  side         TEXT NOT NULL,
  start_line   INTEGER NOT NULL,
  end_line     INTEGER NOT NULL,
  start_side   TEXT NOT NULL DEFAULT '',
  end_side     TEXT NOT NULL DEFAULT '',
  line_content TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL DEFAULT '',
  author       TEXT NOT NULL DEFAULT 'user',
  status       TEXT NOT NULL DEFAULT 'open',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX idx_comments_version ON comments(version_id);
CREATE TABLE replies (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  comment_id   INTEGER NOT NULL REFERENCES comments(id),
  origin       TEXT NOT NULL,
  kind         TEXT NOT NULL,
  body         TEXT NOT NULL DEFAULT '',
  ask_json     TEXT NOT NULL DEFAULT '',
  answered     INTEGER NOT NULL DEFAULT 0,
  answer       TEXT NOT NULL DEFAULT '',
  answered_via TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  dedup_key    TEXT
);
CREATE INDEX idx_replies_comment ON replies(comment_id);
CREATE UNIQUE INDEX idx_replies_dedup ON replies(dedup_key) WHERE dedup_key IS NOT NULL;
CREATE TABLE file_states (
  review_id            TEXT NOT NULL REFERENCES subjects(id),
  path                 TEXT NOT NULL,
  reviewed             INTEGER NOT NULL DEFAULT 0,
  hidden               INTEGER NOT NULL DEFAULT 0,
  reviewed_fingerprint TEXT NOT NULL DEFAULT '',
  updated_at           INTEGER NOT NULL,
  PRIMARY KEY (review_id, path)
);
CREATE TABLE ai_requests (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  review_id      TEXT NOT NULL REFERENCES subjects(id),
  version_number INTEGER NOT NULL,
  source         TEXT NOT NULL,
  prompt         TEXT NOT NULL,
  status         TEXT NOT NULL DEFAULT 'pending',
  summary        TEXT NOT NULL DEFAULT '',
  phase          TEXT NOT NULL DEFAULT '',
  unmatched_json TEXT NOT NULL DEFAULT '[]',
  changes_json   TEXT NOT NULL DEFAULT '[]',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  question_json  TEXT NOT NULL DEFAULT '',
  answer_json    TEXT NOT NULL DEFAULT '',
  attempt        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_ai_requests_review ON ai_requests(review_id);
CREATE TABLE organizations (
  version_id    INTEGER PRIMARY KEY REFERENCES review_versions(id),
  chapters_json TEXT NOT NULL,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE TABLE turn_attributions (
  version_id  INTEGER NOT NULL REFERENCES review_versions(id),
  file_path   TEXT NOT NULL,
  ranges_json TEXT NOT NULL DEFAULT '[]',
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (version_id, file_path)
);
CREATE TABLE annotations (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  version_id    INTEGER NOT NULL REFERENCES review_versions(id),
  file_path     TEXT NOT NULL,
  side          TEXT NOT NULL,
  start_line    INTEGER NOT NULL,
  end_line      INTEGER NOT NULL,
  label         TEXT NOT NULL DEFAULT '',
  ai_request_id INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);
CREATE INDEX idx_annotations_version ON annotations(version_id);
`

const schemaMarkerV1 = `
CREATE TABLE cc_review_schema (
  singleton   INTEGER PRIMARY KEY CHECK(singleton = 1),
  version     INTEGER NOT NULL CHECK(version = 1),
  fingerprint TEXT NOT NULL
);
`

var schemaFingerprintV1 = fmt.Sprintf("%x", sha256.Sum256([]byte(schemaV1)))

// ErrForeignSchema means a database is not the exact cc-review v1 schema.
var ErrForeignSchema = errors.New("cc-review database is not exact schema v1; discard the derived state namespace")

// ApplySchemaV1 creates or verifies the exact review schema in the v1 namespace.
func ApplySchemaV1(ctx context.Context, db *sql.DB) error {
	if err := vcs.TurnsMigrate(ctx, db); err != nil {
		return err
	}
	var markerExists int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cc_review_schema'`).Scan(&markerExists); err != nil {
		return fmt.Errorf("inspect review schema: %w", err)
	}
	if markerExists == 1 {
		var version int
		var fingerprint string
		if err := db.QueryRowContext(ctx,
			`SELECT version, fingerprint FROM cc_review_schema WHERE singleton=1`).Scan(&version, &fingerprint); err != nil {
			return fmt.Errorf("%w: read marker: %w", ErrForeignSchema, err)
		}
		if version != 1 || fingerprint != schemaFingerprintV1 {
			return fmt.Errorf("%w: version=%d fingerprint=%q", ErrForeignSchema, version, fingerprint)
		}
		return nil
	}

	var oldTables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN (
		'review_meta','review_versions','comments','replies','file_states','ai_requests','organizations','turn_attributions','annotations'
	)`).Scan(&oldTables); err != nil {
		return fmt.Errorf("inspect pre-v1 review tables: %w", err)
	}
	if oldTables != 0 {
		return fmt.Errorf("%w: found %d unversioned review tables", ErrForeignSchema, oldTables)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review schema v1: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, schemaMarkerV1+schemaV1); err != nil {
		return fmt.Errorf("apply review schema v1: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO cc_review_schema(singleton, version, fingerprint) VALUES(1,1,?)`, schemaFingerprintV1); err != nil {
		return fmt.Errorf("mark review schema v1: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit review schema v1: %w", err)
	}
	return nil
}

// Open opens the cc-interact core database at path with the review schema applied
// and returns a Store that owns its Close. The daemon does not use this — its
// store is opened inside daemon.New — but the export command and the tests do.
func Open(path string) (*Store, error) {
	cc, err := ccstore.Open(path, ApplySchemaV1)
	if err != nil {
		return nil, err
	}
	return &Store{cc: cc, db: cc.DB()}, nil
}

// New wraps a borrowed connection (the daemon's) for domain CRUD. The caller
// owns the connection lifecycle; Close on such a Store is a no-op.
func New(db *sql.DB) *Store { return &Store{db: db} }

// DB exposes the underlying connection so callers can build sibling stores
// (the turn ledger, the subject CAS) against the same writer.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database when this Store owns it (opened via Open).
func (s *Store) Close() error {
	if s.cc != nil {
		return s.cc.Close()
	}
	return nil
}

// newID returns a random 128-bit identifier as 32 lowercase hex chars.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("read random id: %v", err))
	}
	return hex.EncodeToString(b[:])
}

func unix(t time.Time) int64 { return t.Unix() }

func fromUnix(n int64) time.Time { return time.Unix(n, 0) }
