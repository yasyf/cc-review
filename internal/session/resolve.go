// Package session resolves which review a `start` invocation belongs to, keyed
// on the Claude session id plus the repository root. The branch is deliberately
// never part of the key: a mid-session checkout must not fork the review.
package session

import (
	"context"
	"fmt"

	"github.com/yasyf/cc-review/internal/store"
)

// Opts are the inputs to resolution, straight from the `start` command.
type Opts struct {
	SessionID string
	RepoRoot  string
	Branch    string // names a fresh review's slug; never part of the key
	New       bool   // force a fresh review, detaching any existing session match
}

// Resolve returns the review a start should attach to and whether it is a resume
// (an existing review gaining a new version) versus a fresh create.
//
//  1. exact (session_id, repo_root) match  → resume
//  2. latest open repo-root review         → adopt (reparent to this session) + resume
//  3. otherwise                            → create
//
// Exact match wins over a newer open review, so the reparent in 2 can never
// collide with a binding this session already holds. --new first detaches+closes
// any exact match so the unique (session, repo) slot is free, then creates.
func Resolve(ctx context.Context, st *store.Store, o Opts) (store.Review, bool, error) {
	if o.New {
		if existing, ok, err := st.FindReviewBySessionRepo(ctx, o.SessionID, o.RepoRoot); err != nil {
			return store.Review{}, false, err
		} else if ok {
			if err := st.SetReviewStatus(ctx, existing.ID, "closed"); err != nil {
				return store.Review{}, false, err
			}
			if err := st.DetachReviewSession(ctx, existing.ID); err != nil {
				return store.Review{}, false, err
			}
		}
		return create(ctx, st, o)
	}

	if r, ok, err := st.FindReviewBySessionRepo(ctx, o.SessionID, o.RepoRoot); err != nil {
		return store.Review{}, false, err
	} else if ok {
		return r, true, nil
	}

	if r, ok, err := st.FindLatestOpenReviewByRepo(ctx, o.RepoRoot); err != nil {
		return store.Review{}, false, err
	} else if ok {
		if o.SessionID != "" {
			if err := st.ReparentReviewSession(ctx, r.ID, o.SessionID, "adopt"); err != nil {
				return store.Review{}, false, err
			}
			r.SessionID = o.SessionID
		}
		return r, true, nil
	}

	return create(ctx, st, o)
}

func create(ctx context.Context, st *store.Store, o Opts) (store.Review, bool, error) {
	r, err := st.CreateReview(ctx, o.SessionID, o.RepoRoot, o.Branch)
	if err != nil {
		return store.Review{}, false, fmt.Errorf("create review: %w", err)
	}
	return r, false, nil
}
