// Package session is the single review-ownership resolver. A review belongs to
// a window — one Claude Code process — not to a session id: ids rotate on
// /clear, resume, and compact, while the pid stays put for the window's whole
// life. The branch deliberately never enters the key: it only names a fresh
// review's slug, and a mid-session checkout must not fork the review.
package session

import (
	"context"

	"github.com/yasyf/cc-review/internal/store"
)

// Window identifies one Claude Code process: the current (rotating) session id
// plus the stable pid. ClaudePID 0 means manual CLI use outside any window.
type Window struct {
	SessionID string
	ClaudePID int
}

// Resolver maps windows to the reviews they own. Held reports whether the
// window owning a review is still alive; the daemon supplies it so this
// package stays store-only.
type Resolver struct {
	Store *store.Store
	Held  func(ctx context.Context, r store.Review) bool
}

// Find returns the window's review without ever writing: the exact
// (session id, repo root) binding, else the window's latest review by pid in
// any status — covering a stale channel session id and feedback after submit
// after a session rotation.
func (rs Resolver) Find(ctx context.Context, w Window, repoRoot string) (store.Review, bool, error) {
	if r, ok, err := rs.Store.FindReviewBySessionRepo(ctx, w.SessionID, repoRoot); err != nil || ok {
		return r, ok, err
	}
	return rs.Store.FindLatestReviewByWindowRepo(ctx, w.ClaudePID, repoRoot)
}

// Start returns the review a start attaches to and whether that is a resume
// (an existing review gaining a new version) versus a fresh create:
//
//  1. exact (session id, repo root) binding        → resume
//  2. the window's pid-latest review               → rebind to the new session id, resume
//  3. latest open repo review with no live window  → adopt, resume
//  4. otherwise                                    → create
//
// fresh=true skips adoption: it closes and detaches the window's own review
// (rows 1–2 only), then creates.
func (rs Resolver) Start(ctx context.Context, w Window, repoRoot, branch string, fresh bool) (store.Review, bool, error) {
	if fresh {
		if r, ok, err := rs.Find(ctx, w, repoRoot); err != nil {
			return store.Review{}, false, err
		} else if ok {
			if err := rs.Store.SetReviewStatus(ctx, r.ID, "closed"); err != nil {
				return store.Review{}, false, err
			}
			if err := rs.Store.DetachReviewSession(ctx, r.ID); err != nil {
				return store.Review{}, false, err
			}
		}
		return rs.create(ctx, w, repoRoot, branch)
	}

	if r, ok, err := rs.Store.FindReviewBySessionRepo(ctx, w.SessionID, repoRoot); err != nil {
		return store.Review{}, false, err
	} else if ok {
		if w.ClaudePID != 0 && r.ClaudePID != w.ClaudePID {
			swapped, err := rs.Store.RebindReview(ctx, r.ID, r.ClaudePID, w.SessionID, w.ClaudePID)
			if err != nil {
				return store.Review{}, false, err
			}
			if swapped {
				r.ClaudePID = w.ClaudePID
			} else {
				// CAS miss: a concurrent rebind moved the pid under us; the
				// session binding still holds, so re-read and continue.
				if r, err = rs.Store.GetReview(ctx, r.ID); err != nil {
					return store.Review{}, false, err
				}
			}
		}
		return r, true, nil
	}

	if r, ok, err := rs.Store.FindLatestReviewByWindowRepo(ctx, w.ClaudePID, repoRoot); err != nil {
		return store.Review{}, false, err
	} else if ok {
		swapped, err := rs.Store.RebindReview(ctx, r.ID, r.ClaudePID, w.SessionID, w.ClaudePID)
		if err != nil {
			return store.Review{}, false, err
		}
		if swapped {
			r.SessionID = w.SessionID
			return r, true, nil
		}
	}

	if r, ok, err := rs.Store.FindLatestOpenReviewByRepo(ctx, repoRoot); err != nil {
		return store.Review{}, false, err
	} else if ok && !rs.Held(ctx, r) {
		swapped, err := rs.Store.RebindReview(ctx, r.ID, r.ClaudePID, w.SessionID, w.ClaudePID)
		if err != nil {
			return store.Review{}, false, err
		}
		if swapped {
			r.SessionID = w.SessionID
			r.ClaudePID = w.ClaudePID
			return r, true, nil
		}
	}

	return rs.create(ctx, w, repoRoot, branch)
}

// Rebind follows session rotation at window start (the SessionStart hook): it
// points the window's open review at its new session id, or adopts the repo's
// latest open review when the window holding it is dead. A review held by a
// live foreign window is never stolen; an empty session id is a no-op.
func (rs Resolver) Rebind(ctx context.Context, w Window, repoRoot string) error {
	if w.SessionID == "" {
		return nil
	}

	if _, ok, err := rs.Store.FindReviewBySessionRepo(ctx, w.SessionID, repoRoot); err != nil || ok {
		return err
	}

	if r, ok, err := rs.Store.FindLatestReviewByWindowRepo(ctx, w.ClaudePID, repoRoot); err != nil {
		return err
	} else if ok && r.Status == "open" {
		_, err := rs.Store.RebindReview(ctx, r.ID, r.ClaudePID, w.SessionID, w.ClaudePID)
		return err
	}

	if r, ok, err := rs.Store.FindLatestOpenReviewByRepo(ctx, repoRoot); err != nil {
		return err
	} else if ok && !rs.Held(ctx, r) {
		// A CAS miss means a racing adopter won; this window creates its own on start.
		_, err := rs.Store.RebindReview(ctx, r.ID, r.ClaudePID, w.SessionID, w.ClaudePID)
		return err
	}

	return nil
}

func (rs Resolver) create(ctx context.Context, w Window, repoRoot, branch string) (store.Review, bool, error) {
	r, err := rs.Store.CreateReview(ctx, w.SessionID, w.ClaudePID, repoRoot, branch)
	return r, false, err
}
