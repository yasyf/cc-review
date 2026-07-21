package daemon

import (
	"context"
	"fmt"
	"time"

	ccd "github.com/yasyf/cc-interact/daemon"
	ccevent "github.com/yasyf/cc-interact/event"

	"github.com/yasyf/cc-review/internal/store"
)

// sweepStaleOpen expires open reviews idle past the cutoff — never Detach, an
// expired review must stay bound so an explicit start can reopen it — then
// emits status.changed so an open browser tab learns. The CAS re-checks status
// and idleness, so a Submit, close, or fresh activity landing mid-sweep aborts
// that row's expiry. It returns the expired rows with their pre-expiry idle
// anchor — the status.changed just emitted resets the stored one — so
// closeStale can report how long each review actually sat.
func (rv *review) sweepStaleOpen(ctx context.Context, st *store.Store, ap ccd.AppendFunc, before time.Time) ([]store.ReviewListing, error) {
	stale, err := st.StaleOpenReviews(ctx, before)
	if err != nil {
		return nil, err
	}
	var expired []store.ReviewListing
	for _, r := range stale {
		swapped, err := st.ExpireReview(ctx, r.ID, before)
		if err != nil {
			return expired, err
		}
		if !swapped {
			continue
		}
		expired = append(expired, r)
		rv.log.Printf("expired review %s (idle since %s)", r.Slug, r.LastActivity.Format(time.RFC3339))
		v, ok, err := st.LatestVersion(ctx, r.ID)
		if err != nil {
			return expired, err
		}
		if !ok {
			continue
		}
		emit(ctx, ap, r.ID, ccevent.OriginSystem, store.EventStatusChanged, v.VersionNumber,
			map[string]any{"status": statusExpired})
	}
	return expired, nil
}

// handleClose terminally closes a review: the current window's by default, any
// review by slug/id ref, or — with stale — every expired review across scopes.
// Only an explicit ref or stale may touch another window's review; the no-ref
// arm stays window-scoped by construction.
func (rv *review) handleClose(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	b, err := decodeBody(hc.Env.Body)
	if err != nil {
		return errReply(err.Error())
	}
	if b.Stale {
		return rv.closeStale(hc, st)
	}
	var id string
	if b.Ref != "" {
		r, err := st.GetReviewByRef(hc.Ctx, b.Ref)
		if err != nil {
			return errReply(fmt.Sprintf("review %q: %v", b.Ref, err))
		}
		id = r.ID
	} else {
		sub, ok, err := hc.Subjects.Find(hc.Ctx, hc.Window, hc.Scope)
		if err != nil {
			return errReply(err.Error())
		}
		if !ok {
			return errReply("no review for this window; pass a review slug/id or --stale")
		}
		id = sub.ID
	}
	info, err := rv.closeOne(hc, st, id)
	if err != nil {
		return errReply(err.Error())
	}
	return okReply(result{Closed: []ReviewInfo{info}})
}

// closeStale expires every idle-open review, then terminally closes every
// expired review — including ones the daemon's own sweeps expired earlier — so
// the repair command always reports the stale state it cleared.
func (rv *review) closeStale(hc ccd.HandlerCtx, st *store.Store) ccd.Reply {
	swept, err := rv.sweepStaleOpen(hc.Ctx, st, hc.Append, time.Now().Add(-reviewIdleTTL))
	if err != nil {
		return errReply(err.Error())
	}
	// Rows expired just now report their pre-expiry idle anchor; rows the
	// daemon's own sweeps expired earlier report idle-since-expiry.
	idleSince := make(map[string]time.Time, len(swept))
	for _, r := range swept {
		idleSince[r.ID] = r.LastActivity
	}
	rows, err := st.ListReviews(hc.Ctx)
	if err != nil {
		return errReply(err.Error())
	}
	var closed []ReviewInfo
	for _, r := range rows {
		if r.Status != statusExpired {
			continue
		}
		info, err := rv.closeOne(hc, st, r.ID)
		if err != nil {
			return errReply(err.Error())
		}
		info.LastActivity = r.LastActivity
		if at, ok := idleSince[r.ID]; ok {
			info.LastActivity = at
		}
		closed = append(closed, info)
	}
	return okReply(result{Closed: closed})
}

// closeOne closes the review through the shared close pipeline — closed means
// never resumed, mirroring a fresh start's prior-close — then emits
// status.changed.
func (rv *review) closeOne(hc ccd.HandlerCtx, st *store.Store, id string) (ReviewInfo, error) {
	sub, err := hc.Subjects.Store.Get(hc.Ctx, id)
	if err != nil {
		return ReviewInfo{}, err
	}
	swapped, err := st.CloseAndDetach(hc.Ctx, hc.Subjects.Store, id)
	if err != nil {
		return ReviewInfo{}, err
	}
	if !swapped {
		return ReviewInfo{}, fmt.Errorf("review %s is %s; only an open or expired review can be closed", sub.Slug, sub.Status)
	}
	if v, ok, err := st.LatestVersion(hc.Ctx, id); err != nil {
		return ReviewInfo{}, err
	} else if ok {
		emit(hc.Ctx, hc.Append, id, ccevent.OriginHuman, store.EventStatusChanged, v.VersionNumber,
			map[string]any{"status": lifecycle.Closed})
	}
	return ReviewInfo{
		ID: id, Slug: sub.Slug, Scope: sub.Scope, Status: lifecycle.Closed, CreatedAt: sub.CreatedAt,
	}, nil
}

// handleList reports every open or expired review across scopes with its idle
// anchor — the repair surface for seeing what is blocking or lingering.
func (rv *review) handleList(hc ccd.HandlerCtx) ccd.Reply {
	st := store.New(hc.DB)
	rows, err := st.ListReviews(hc.Ctx)
	if err != nil {
		return errReply(err.Error())
	}
	return okReply(result{Reviews: reviewInfos(rows)})
}

func reviewInfos(rows []store.ReviewListing) []ReviewInfo {
	out := make([]ReviewInfo, len(rows))
	for i, r := range rows {
		out[i] = ReviewInfo{
			ID: r.ID, Slug: r.Slug, Scope: r.Scope, Status: r.Status,
			CreatedAt: r.CreatedAt, LastActivity: r.LastActivity,
		}
	}
	return out
}
