package daemon

import (
	"context"

	ccd "github.com/yasyf/cc-interact/daemon"

	"github.com/yasyf/cc-review/internal/prsync"
	"github.com/yasyf/cc-review/internal/store"
)

func (rv *review) newPRSync(s *ccd.Server) *prsync.Syncer {
	return prsync.New(prsync.Config{
		DB:     s.DB,
		Append: s.Append,
		Client: rv.gh,
		Watched: func(reviewID string) bool {
			return s.ConsumerConnected(reviewID) || s.ViewerConnected(reviewID)
		},
		Recapture:  rv.recapturePR,
		Background: s.Background,
		Log:        rv.log,
	})
}

func (rv *review) startPRSync(ctx context.Context, reviewID string) {
	if err := rv.prsync.Start(ctx, reviewID); err != nil {
		rv.log.Printf("prsync %s: %v", reviewID, err)
	}
}

func (rv *review) resumePRSync(ctx context.Context, st *store.Store) {
	ids, err := st.SyncablePRReviews(ctx)
	if err != nil {
		rv.log.Printf("prsync resume: %v", err)
		return
	}
	for _, id := range ids {
		rv.startPRSync(ctx, id)
	}
}
