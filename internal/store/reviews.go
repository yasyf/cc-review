package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yasyf/cc-interact/subject"
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

// ReviewMeta is a review's pinned diff base and creation-time branch. Stack
// marks a Graphite stacked review, which pins no base and re-detects its
// sections on every resume.
type ReviewMeta struct {
	BaseRef string
	Branch  string
	Stack   bool
}

// ReviewSlug derives a review's URL name as the first 8 hex chars of a fresh
// random hash.
func ReviewSlug(hash string) string { return hash[:8] }

// NewSlugHash returns a fresh random hash for ReviewSlug. The subject id is
// generated inside the resolver, so the slug carries its own uniqueness suffix.
func NewSlugHash() string { return newID() }

// GetReviewByRef returns the review named by ref — a slug (what the browser
// sends) or a full id (what the Claude-side stream consumers send) — or
// ErrNotFound. The namespaces cannot collide: slugs are 8 hex chars (legacy
// slugs also contain `--`), ids are 32 hex chars.
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

// SetReviewMeta upserts a review's pinned diff base, creation branch, and
// stack flag.
func (s *Store) SetReviewMeta(ctx context.Context, subjectID, baseRef, branch string, stack bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_meta(subject_id, base_ref, branch, stack) VALUES(?,?,?,?)
		 ON CONFLICT(subject_id) DO UPDATE SET base_ref=excluded.base_ref, branch=excluded.branch, stack=excluded.stack`,
		subjectID, baseRef, branch, boolInt(stack))
	if err != nil {
		return errors.New("set review meta: " + err.Error())
	}
	return nil
}

// GetReviewMeta returns a review's pinned base, branch, and stack flag; ok is
// false when no meta row exists yet (a subject created before its base was
// pinned).
func (s *Store) GetReviewMeta(ctx context.Context, subjectID string) (ReviewMeta, bool, error) {
	var (
		m     ReviewMeta
		stack int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT base_ref, branch, stack FROM review_meta WHERE subject_id=?`, subjectID).Scan(&m.BaseRef, &m.Branch, &stack)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewMeta{}, false, nil
	}
	m.Stack = stack != 0
	return m, err == nil, err
}

// ReviewListing is one open or expired review row with its idle anchor: the
// newest real event — anything but channel presence — falling back to creation
// time.
type ReviewListing struct {
	ID           string
	Slug         string
	Scope        string
	Status       string
	CreatedAt    time.Time
	LastActivity time.Time
}

// idleAnchorSQL is the review idle clock: the newest event excluding
// channel.changed — presence pings arrive on every session attach and would
// keep an abandoned review looking fresh forever — falling back to creation
// time. Callers bind EventChannelChanged for its placeholder.
const idleAnchorSQL = `COALESCE((SELECT MAX(e.created_at) FROM events e
	WHERE e.subject_id = %[1]s.id AND e.type <> ?), %[1]s.created_at)`

// reviewListingSQL projects the subjects matching statusPredicate with their
// last real activity. The subselect exists because SQLite cannot reference the
// alias in an outer WHERE.
func reviewListingSQL(statusPredicate string) string {
	return `
	SELECT id, slug, scope, status, created_at, last_activity FROM (
		SELECT s.id, s.slug, s.scope, s.status, s.created_at,
		       ` + fmt.Sprintf(idleAnchorSQL, "s") + ` AS last_activity
		FROM subjects s WHERE ` + statusPredicate + `
	)`
}

// ListReviews returns every open or expired review ordered by last real
// activity — the repair surface for seeing what is blocking or lingering.
func (s *Store) ListReviews(ctx context.Context) ([]ReviewListing, error) {
	return s.scanReviewListings(ctx,
		reviewListingSQL(`s.status IN ('open','expired')`)+` ORDER BY last_activity`,
		EventChannelChanged)
}

// StaleOpenReviews returns the open reviews whose last real activity predates
// before — the set the sweeper expires.
func (s *Store) StaleOpenReviews(ctx context.Context, before time.Time) ([]ReviewListing, error) {
	return s.scanReviewListings(ctx,
		reviewListingSQL(`s.status = 'open'`)+` WHERE last_activity < ? ORDER BY last_activity`,
		EventChannelChanged, unix(before))
}

func (s *Store) scanReviewListings(ctx context.Context, query string, args ...any) ([]ReviewListing, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ReviewListing
	for rows.Next() {
		var (
			r                 ReviewListing
			created, activity int64
		)
		if err := rows.Scan(&r.ID, &r.Slug, &r.Scope, &r.Status, &created, &activity); err != nil {
			return nil, err
		}
		r.CreatedAt, r.LastActivity = fromUnix(created), fromUnix(activity)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExpireReview moves an open review to expired. The CAS re-checks both status
// and idleness inside the UPDATE, so activity landing between the sweeper's
// scan and this write aborts the expiry; false means done, not failed.
func (s *Store) ExpireReview(ctx context.Context, id string, before time.Time) (bool, error) {
	return oneRow(s.db.ExecContext(ctx, `
		UPDATE subjects SET status='expired', updated_at=?
		WHERE id=? AND status='open'
		  AND `+fmt.Sprintf(idleAnchorSQL, "subjects")+` < ?`,
		unix(time.Now()), id, EventChannelChanged, unix(before)))
}

// CloseReview terminally closes an open or expired review; false when it is
// already submitted or closed.
func (s *Store) CloseReview(ctx context.Context, id string) (bool, error) {
	return oneRow(s.db.ExecContext(ctx,
		`UPDATE subjects SET status='closed', updated_at=? WHERE id=? AND status IN ('open','expired')`,
		unix(time.Now()), id))
}

// CloseAndDetach terminally closes an open or expired review and detaches it
// so it is never resumed; false (already submitted or closed) changes nothing.
// The one close pipeline shared by the daemon op and the REST endpoint.
func (s *Store) CloseAndDetach(ctx context.Context, subjects subject.Store, id string) (bool, error) {
	swapped, err := s.CloseReview(ctx, id)
	if err != nil || !swapped {
		return swapped, err
	}
	if err := subjects.Detach(ctx, id); err != nil {
		return true, fmt.Errorf("detach closed review: %w", err)
	}
	return true, nil
}

func oneRow(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, fmt.Errorf("transition review status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("transition review status: %w", err)
	}
	return n == 1, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
