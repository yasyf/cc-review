// Package thinstore keeps one blobless bare clone per GitHub repository so a
// pull request's diff fetches only the blobs of the files it changes.
package thinstore

import (
	"context"

	"github.com/yasyf/cc-review/internal/github"
)

// Store is a blobless bare clone under paths.Repos().
type Store struct {
	Dir string
}

// Open clones repo from cloneURL into its store when absent and verifies the
// store still fetches blobs lazily from it.
func Open(ctx context.Context, repo github.Repo, cloneURL string) (*Store, error) {
	panic("TODO")
}

// FetchPR fetches pull request number's head into refs/cc-review/pr/<number>
// and returns its sha.
func (s *Store) FetchPR(ctx context.Context, number int) (string, error) {
	panic("TODO")
}

// FetchCommit makes sha present in the store.
func (s *Store) FetchCommit(ctx context.Context, sha string) error {
	panic("TODO")
}
