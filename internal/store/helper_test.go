package store

import (
	"context"
	"testing"

	ccstore "github.com/yasyf/cc-interact/store"
	"github.com/yasyf/cc-interact/subject"
)

// newSubjectStoreForTest opens the cc-interact subject store over the same
// writer connection, for tests that need a subject's slug as well as its id.
func newSubjectStoreForTest(s *Store) subject.Store {
	return ccstore.NewSubjectStore(s.DB(), []string{"open"})
}

// seedReview creates a review subject and its pinned meta, returning the subject
// id. It is the test-side stand-in for the daemon's resolver, which now lives in
// cc-interact and is tested there.
func seedReview(t *testing.T, s *Store, session string, pid int, repo, branch, base string) string {
	t.Helper()
	ss := ccstore.NewSubjectStore(s.DB(), []string{"open"})
	sub, err := ss.Create(context.Background(), NewSlugHash(), ReviewSlug(branch, NewSlugHash()), session, repo, pid, "open")
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}
	if err := s.SetReviewMeta(context.Background(), sub.ID, base, branch); err != nil {
		t.Fatalf("seed meta: %v", err)
	}
	return sub.ID
}

// setReviewStatus moves a review subject's lifecycle status (e.g. to
// "submitted") via the cc-interact subject store.
func setReviewStatus(t *testing.T, s *Store, id, status string) {
	t.Helper()
	if err := ccstore.NewSubjectStore(s.DB(), []string{"open"}).SetStatus(context.Background(), id, status); err != nil {
		t.Fatalf("set review status: %v", err)
	}
}
