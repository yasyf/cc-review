package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Review is the read model the HTTP plane renders: a subject projected through
// the review domain. Ownership, lifecycle, and creation live on the core
// subjects table; the pinned base and branch live on review_meta.
type Review struct {
	ID        string
	Status    string
	RepoRoot  string
	CreatedAt time.Time
}

// ReviewMeta is a review's pinned diff base and creation-time branch.
type ReviewMeta struct {
	BaseRef string
	Branch  string
}

// ReviewSlug derives a review's URL name from its creation-time branch and a
// random hash: the sanitized branch, `--`, and the first 8 hex chars of the
// hash. An empty branch (detached HEAD) yields just the hash prefix.
func ReviewSlug(branch, hash string) string {
	h := hash[:8]
	if branch == "" {
		return h
	}
	return sanitizeBranch(branch) + "--" + h
}

// sanitizeBranch makes a branch name URL-safe: `/` becomes `--`, and any other
// rune outside [A-Za-z0-9._-] becomes `-` (git allows `#`, `%` etc., which
// break URLs; the hash suffix keeps slugs unique regardless).
func sanitizeBranch(branch string) string {
	var b strings.Builder
	for _, r := range branch {
		switch {
		case r == '/':
			b.WriteString("--")
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// NewSlugHash returns a fresh random hash for ReviewSlug. The subject id is
// generated inside the resolver, so the slug carries its own uniqueness suffix.
func NewSlugHash() string { return newID() }

// GetReviewByRef returns the review named by ref — a slug (what the browser
// sends) or a full id (what the Claude-side stream consumers send) — or
// ErrNotFound. The namespaces cannot collide: slugs are 8 chars or contain
// `--`, ids are 32 hex chars.
func (s *Store) GetReviewByRef(ctx context.Context, ref string) (Review, error) {
	return s.scanReview(s.db.QueryRowContext(ctx,
		`SELECT id, status, scope, created_at FROM subjects WHERE slug=? OR id=?`, ref, ref))
}

// GetReview returns the review by subject id, or ErrNotFound.
func (s *Store) GetReview(ctx context.Context, id string) (Review, error) {
	return s.scanReview(s.db.QueryRowContext(ctx,
		`SELECT id, status, scope, created_at FROM subjects WHERE id=?`, id))
}

func (s *Store) scanReview(row interface{ Scan(...any) error }) (Review, error) {
	var (
		r       Review
		created int64
	)
	if err := row.Scan(&r.ID, &r.Status, &r.RepoRoot, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Review{}, ErrNotFound
		}
		return Review{}, err
	}
	r.CreatedAt = fromUnix(created)
	return r, nil
}

// SetReviewMeta upserts a review's pinned diff base and creation branch.
func (s *Store) SetReviewMeta(ctx context.Context, subjectID, baseRef, branch string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_meta(subject_id, base_ref, branch) VALUES(?,?,?)
		 ON CONFLICT(subject_id) DO UPDATE SET base_ref=excluded.base_ref, branch=excluded.branch`,
		subjectID, baseRef, branch)
	if err != nil {
		return errors.New("set review meta: " + err.Error())
	}
	return nil
}

// GetReviewMeta returns a review's pinned base and branch; ok is false when no
// meta row exists yet (a subject created before its base was pinned).
func (s *Store) GetReviewMeta(ctx context.Context, subjectID string) (ReviewMeta, bool, error) {
	var m ReviewMeta
	err := s.db.QueryRowContext(ctx,
		`SELECT base_ref, branch FROM review_meta WHERE subject_id=?`, subjectID).Scan(&m.BaseRef, &m.Branch)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewMeta{}, false, nil
	}
	return m, err == nil, err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
