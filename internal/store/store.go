// Package store is cc-review's sole state layer: a modernc.org/sqlite (pure-Go)
// append-only database holding reviews, their diff versions, inline comments,
// the back-and-forth replies, and the single per-review event log that drives
// the realtime fan-out. Rows are never deleted; status flags carry state. Large
// patches live on disk (see internal/paths); the DB keeps only the patch path
// and a files summary.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the single-writer sqlite connection.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS reviews (
  id         TEXT PRIMARY KEY,
  slug       TEXT NOT NULL DEFAULT '',
  session_id TEXT,
  repo_root  TEXT NOT NULL,
  base_ref   TEXT NOT NULL DEFAULT '',
  claude_pid INTEGER NOT NULL DEFAULT 0,
  status     TEXT NOT NULL DEFAULT 'open',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_session_repo
  ON reviews(session_id, repo_root) WHERE session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reviews_repo ON reviews(repo_root);
CREATE INDEX IF NOT EXISTS idx_reviews_pid_repo ON reviews(claude_pid, repo_root);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_slug ON reviews(slug) WHERE slug <> '';
CREATE TABLE IF NOT EXISTS review_versions (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  review_id      TEXT NOT NULL REFERENCES reviews(id),
  version_number INTEGER NOT NULL,
  branch         TEXT NOT NULL DEFAULT '',
  base_ref       TEXT NOT NULL DEFAULT '',
  patch_path     TEXT NOT NULL,
  files_json     TEXT NOT NULL DEFAULT '[]',
  created_at     INTEGER NOT NULL,
  UNIQUE(review_id, version_number)
);
CREATE TABLE IF NOT EXISTS comments (
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
CREATE INDEX IF NOT EXISTS idx_comments_version ON comments(version_id);
CREATE TABLE IF NOT EXISTS replies (
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
CREATE INDEX IF NOT EXISTS idx_replies_comment ON replies(comment_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_replies_dedup ON replies(dedup_key) WHERE dedup_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS file_states (
  review_id            TEXT NOT NULL REFERENCES reviews(id),
  path                 TEXT NOT NULL,
  reviewed             INTEGER NOT NULL DEFAULT 0,
  hidden               INTEGER NOT NULL DEFAULT 0,
  reviewed_fingerprint TEXT NOT NULL DEFAULT '',
  updated_at           INTEGER NOT NULL,
  PRIMARY KEY (review_id, path)
);
CREATE TABLE IF NOT EXISTS ai_requests (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  review_id      TEXT NOT NULL REFERENCES reviews(id),
  version_number INTEGER NOT NULL,
  source         TEXT NOT NULL,
  prompt         TEXT NOT NULL,
  status         TEXT NOT NULL DEFAULT 'pending',
  summary        TEXT NOT NULL DEFAULT '',
  unmatched_json TEXT NOT NULL DEFAULT '[]',
  changes_json   TEXT NOT NULL DEFAULT '[]',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ai_requests_review ON ai_requests(review_id);
CREATE TABLE IF NOT EXISTS organizations (
  version_id    INTEGER PRIMARY KEY REFERENCES review_versions(id),
  chapters_json TEXT NOT NULL,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  review_id      TEXT NOT NULL REFERENCES reviews(id),
  seq            INTEGER NOT NULL,
  origin         TEXT NOT NULL,
  type           TEXT NOT NULL,
  version_number INTEGER NOT NULL DEFAULT 0,
  payload        TEXT NOT NULL DEFAULT '{}',
  created_at     INTEGER NOT NULL,
  dedup_key      TEXT,
  PRIMARY KEY (review_id, seq)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedup ON events(review_id, dedup_key) WHERE dedup_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS turns (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  repo_root         TEXT NOT NULL,
  backend           TEXT NOT NULL DEFAULT 'git',
  session_id        TEXT NOT NULL DEFAULT '',
  claude_pid        INTEGER NOT NULL DEFAULT 0,
  prompt_excerpt    TEXT NOT NULL DEFAULT '',
  transcript_path   TEXT NOT NULL DEFAULT '',
  transcript_offset INTEGER NOT NULL DEFAULT -1,
  tree_start        TEXT NOT NULL,
  tree_end          TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'open',
  started_at        INTEGER NOT NULL,
  ended_at          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_turns_repo ON turns(repo_root, id);
CREATE INDEX IF NOT EXISTS idx_turns_repo_open ON turns(repo_root, claude_pid) WHERE status='open';
CREATE TABLE IF NOT EXISTS turn_attributions (
  version_id  INTEGER NOT NULL REFERENCES review_versions(id),
  file_path   TEXT NOT NULL,
  ranges_json TEXT NOT NULL DEFAULT '[]',
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (version_id, file_path)
);
`

// Open opens (creating if needed) the database at path and applies the schema.
// A single serialized writer (SetMaxOpenConns(1)) with WAL avoids "database is
// locked" across the SSE fan-out, REST, and the event bus. There are no
// migrations: on a schema change, wipe the local state dir.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

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
