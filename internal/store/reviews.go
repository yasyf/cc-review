package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	if err := row.Scan(&r.ID, &session, &r.RepoRoot, &r.Status, &created, &updated); err != nil {
		return Review{}, err
	}
	r.SessionID = session.String
	r.CreatedAt = fromUnix(created)
	r.UpdatedAt = fromUnix(updated)
	return r, nil
}

const reviewCols = `id, session_id, repo_root, status, created_at, updated_at`

// CreateReview inserts a new open review. A blank sessionID is stored as NULL so
// the partial unique index does not collapse all session-less reviews together.
func (s *Store) CreateReview(ctx context.Context, sessionID, repoRoot string) (Review, error) {
	now := time.Now()
	r := Review{ID: newID(), SessionID: sessionID, RepoRoot: repoRoot, Status: "open", CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews(id, session_id, repo_root, status, created_at, updated_at) VALUES(?,?,?,?,?,?)`,
		r.ID, nullString(sessionID), repoRoot, r.Status, unix(now), unix(now))
	if err != nil {
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
		`SELECT `+reviewCols+` FROM reviews WHERE repo_root=? AND status='open' ORDER BY created_at DESC LIMIT 1`, repoRoot)
	r, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, false, nil
	}
	return r, err == nil, err
}

// BackfillSessionID attaches a session id to a previously session-less review.
func (s *Store) BackfillSessionID(ctx context.Context, reviewID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE reviews SET session_id=?, updated_at=? WHERE id=?`, sessionID, unix(time.Now()), reviewID)
	if err != nil {
		return fmt.Errorf("backfill session id: %w", err)
	}
	return nil
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

// DetachReviewSession clears a review's session id (back to NULL), freeing the
// (session_id, repo_root) slot so a fresh review can take it. Used by --new.
func (s *Store) DetachReviewSession(ctx context.Context, reviewID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE reviews SET session_id=NULL, updated_at=? WHERE id=?`, unix(time.Now()), reviewID)
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
