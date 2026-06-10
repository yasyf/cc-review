package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a lookup by id finds no row.
var ErrNotFound = errors.New("not found")

func scanReview(row interface{ Scan(...any) error }) (Review, error) {
	var (
		r       Review
		session sql.NullString
		created int64
		updated int64
	)
	if err := row.Scan(&r.ID, &r.Slug, &session, &r.RepoRoot, &r.ClaudePID, &r.Status, &created, &updated); err != nil {
		return Review{}, err
	}
	r.SessionID = session.String
	r.CreatedAt = fromUnix(created)
	r.UpdatedAt = fromUnix(updated)
	return r, nil
}

const reviewCols = `id, slug, session_id, repo_root, claude_pid, status, created_at, updated_at`

// ReviewSlug derives a review's URL name from its creation-time branch and id:
// the sanitized branch, `--`, and the first 8 hex chars of the id. An empty
// branch (detached HEAD) yields just the id prefix.
func ReviewSlug(branch, id string) string {
	hash := id[:8]
	if branch == "" {
		return hash
	}
	return sanitizeBranch(branch) + "--" + hash
}

// sanitizeBranch makes a branch name URL-safe: `/` becomes `--`, and any other
// rune outside [A-Za-z0-9._-] becomes `-` (git allows `#`, `%` etc., which
// break URLs; the id-prefix suffix keeps slugs unique regardless).
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

// CreateReview inserts a new open review owned by a Claude window (claudePID)
// in a repo. A blank sessionID is stored as NULL so the partial unique index
// does not collapse all session-less reviews together. branch names the slug;
// it is fixed at creation even if later versions land on another branch.
func (s *Store) CreateReview(ctx context.Context, sessionID string, claudePID int, repoRoot, branch string) (Review, error) {
	now := time.Now()
	r := Review{ID: newID(), SessionID: sessionID, RepoRoot: repoRoot, ClaudePID: claudePID, Status: "open", CreatedAt: now, UpdatedAt: now}
	r.Slug = ReviewSlug(branch, r.ID)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews(id, slug, session_id, repo_root, claude_pid, status, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		r.ID, r.Slug, nullString(sessionID), repoRoot, claudePID, r.Status, unix(now), unix(now)); err != nil {
		return Review{}, fmt.Errorf("create review: %w", err)
	}
	return r, nil
}

// GetReview returns the review by id, or ErrNotFound.
func (s *Store) GetReview(ctx context.Context, id string) (Review, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+reviewCols+` FROM reviews WHERE id=?`, id)
	r, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, ErrNotFound
	}
	return r, err
}

// GetReviewByRef returns the review named by ref — a slug (what the browser
// sends) or a full id (what the Claude-side stream consumers send) — or
// ErrNotFound. The namespaces cannot collide: slugs are 8 chars or contain
// `--`, ids are 32 hex chars.
func (s *Store) GetReviewByRef(ctx context.Context, ref string) (Review, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+reviewCols+` FROM reviews WHERE slug=? OR id=?`, ref, ref)
	r, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, ErrNotFound
	}
	return r, err
}

// FindReviewBySessionRepo returns the review for an exact (session_id, repo_root)
// pair. ok is false when none exists.
func (s *Store) FindReviewBySessionRepo(ctx context.Context, sessionID, repoRoot string) (Review, bool, error) {
	if sessionID == "" {
		return Review{}, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewCols+` FROM reviews WHERE session_id=? AND repo_root=?`, sessionID, repoRoot)
	r, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, false, nil
	}
	return r, err == nil, err
}

// FindLatestOpenReviewByRepo returns the most recent open review for a repo root
// regardless of session id, for explicit adoption. ok is false when none exists.
func (s *Store) FindLatestOpenReviewByRepo(ctx context.Context, repoRoot string) (Review, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewCols+` FROM reviews WHERE repo_root=? AND status='open' ORDER BY created_at DESC, rowid DESC LIMIT 1`, repoRoot)
	r, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, false, nil
	}
	return r, err == nil, err
}

// FindLatestReviewByWindowRepo returns the most recent review owned by a live
// Claude window (claudePID) in a repo, any status — callers filter. ok is
// false when none exists; claudePID 0 means detached and never matches.
func (s *Store) FindLatestReviewByWindowRepo(ctx context.Context, claudePID int, repoRoot string) (Review, bool, error) {
	if claudePID == 0 {
		return Review{}, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewCols+` FROM reviews WHERE claude_pid=? AND repo_root=? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		claudePID, repoRoot)
	r, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, false, nil
	}
	return r, err == nil, err
}

// RebindReview compare-and-swaps a review's owning window: it rebinds
// session_id and claude_pid only if the review's claude_pid still equals
// fromPID, returning whether the swap landed. There is no status gate —
// resuming a submitted review across session rotation is legitimate. A
// unique-index violation (sessionID already owns another review in the repo)
// propagates as the error.
func (s *Store) RebindReview(ctx context.Context, reviewID string, fromPID int, sessionID string, claudePID int) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE reviews SET session_id=?, claude_pid=?, updated_at=? WHERE id=? AND claude_pid=?`,
		nullString(sessionID), claudePID, unix(time.Now()), reviewID, fromPID)
	if err != nil {
		return false, fmt.Errorf("rebind review: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rebind review: %w", err)
	}
	return n == 1, nil
}

// SetReviewStatus updates a review's status and bumps updated_at.
func (s *Store) SetReviewStatus(ctx context.Context, reviewID, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE reviews SET status=?, updated_at=? WHERE id=?`, status, unix(time.Now()), reviewID)
	if err != nil {
		return fmt.Errorf("set review status: %w", err)
	}
	return nil
}

// DetachReviewSession clears a review's session id (back to NULL) and zeroes
// claude_pid, freeing both the (session_id, repo_root) slot and the window
// binding so a fresh review can take them. Used by --new.
func (s *Store) DetachReviewSession(ctx context.Context, reviewID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE reviews SET session_id=NULL, claude_pid=0, updated_at=? WHERE id=?`, unix(time.Now()), reviewID)
	if err != nil {
		return fmt.Errorf("detach review session: %w", err)
	}
	return nil
}

// ReviewStatus returns just the status string for a review, or ErrNotFound.
func (s *Store) ReviewStatus(ctx context.Context, reviewID string) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM reviews WHERE id=?`, reviewID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
