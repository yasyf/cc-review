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
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS reviews (
  id         TEXT PRIMARY KEY,
  slug       TEXT NOT NULL DEFAULT '',
  session_id TEXT,
  repo_root  TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'open',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_session_repo
  ON reviews(session_id, repo_root) WHERE session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reviews_repo ON reviews(repo_root);
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
  options_json TEXT NOT NULL DEFAULT '[]',
  answered     INTEGER NOT NULL DEFAULT 0,
  answer       TEXT NOT NULL DEFAULT '',
  answered_via TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  dedup_key    TEXT
);
CREATE INDEX IF NOT EXISTS idx_replies_comment ON replies(comment_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_replies_dedup ON replies(dedup_key) WHERE dedup_key IS NOT NULL;
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
CREATE TABLE IF NOT EXISTS session_hooks (
  session_id      TEXT PRIMARY KEY,
  cwd             TEXT NOT NULL DEFAULT '',
  transcript_path TEXT NOT NULL DEFAULT '',
  started_at      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS review_sessions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  review_id  TEXT NOT NULL REFERENCES reviews(id),
  session_id TEXT NOT NULL,
  source     TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_review_sessions_review ON review_sessions(review_id);
`

// Open opens (creating if needed) the database at path and applies the schema.
// A single serialized writer (SetMaxOpenConns(1)) with WAL avoids "database is
// locked" across the SSE fan-out, REST, and the event bus.
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
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// migrate brings pre-slug databases up to the current schema: the slug column
// must exist before its unique index, and the backfill (keyed on slug=”) makes
// every run idempotent.
func migrate(db *sql.DB) error {
	var hasSlug int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('reviews') WHERE name='slug'`).Scan(&hasSlug); err != nil {
		return fmt.Errorf("inspect reviews schema: %w", err)
	}
	if hasSlug == 0 {
		if _, err := db.Exec(`ALTER TABLE reviews ADD COLUMN slug TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add slug column: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_slug ON reviews(slug) WHERE slug <> ''`); err != nil {
		return fmt.Errorf("create slug index: %w", err)
	}
	return backfillSlugs(db)
}

func backfillSlugs(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("backfill slugs: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT r.id, COALESCE(
		(SELECT v.branch FROM review_versions v WHERE v.review_id = r.id ORDER BY v.version_number ASC LIMIT 1), '')
		FROM reviews r WHERE r.slug = ''`)
	if err != nil {
		return fmt.Errorf("backfill slugs: %w", err)
	}
	slugs := map[string]string{}
	for rows.Next() {
		var id, branch string
		if err := rows.Scan(&id, &branch); err != nil {
			rows.Close()
			return fmt.Errorf("backfill slugs: %w", err)
		}
		slugs[id] = ReviewSlug(branch, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("backfill slugs: %w", err)
	}
	rows.Close()
	for id, slug := range slugs {
		if _, err := tx.Exec(`UPDATE reviews SET slug=? WHERE id=?`, slug, id); err != nil {
			return fmt.Errorf("backfill slug for %s: %w", id, err)
		}
	}
	return tx.Commit()
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
