// Package paths owns cc-review's domain artifact layout on top of cc-interact's
// canonical state-directory layout (~/.cc-review). The substrate paths (socket,
// db, http handshake, locks, turns, per-consumer cursors) come from App(); this
// package adds the per-review snapshot/feedback files under the same state dir.
package paths

import (
	"fmt"
	"path/filepath"

	ccpaths "github.com/yasyf/cc-interact/paths"
)

// app is the state-dir basename shared by every cc-review process.
const app = ".cc-review"

// App is the cc-interact state-directory layout for cc-review: the socket, db,
// http handshake, lock dir, turn-snapshot scratch, and per-consumer cursors.
func App() ccpaths.Paths { return ccpaths.Paths{App: app} }

// StateDir is cc-review's private state directory (~/.cc-review).
func StateDir() string { return App().StateDir() }

// EnsureStateDir creates ~/.cc-review (0700) if missing.
func EnsureStateDir() error { return App().EnsureStateDir() }

// ReviewDir is the on-disk artifact directory for a single review, the same dir
// cc-interact keys per-subject artifacts (cursors) under.
func ReviewDir(reviewID string) string { return App().SubjectDir(reviewID) }

// SnapshotPath is the patch file for version v of a review.
func SnapshotPath(reviewID string, version int) string {
	return filepath.Join(ReviewDir(reviewID), fmt.Sprintf("snap_%d.patch", version))
}

// FeedbackPath is the frozen feedback JSON for version v of a review.
func FeedbackPath(reviewID string, version int) string {
	return filepath.Join(ReviewDir(reviewID), fmt.Sprintf("feedback_%d.json", version))
}

// EnsureReviewDir creates a review's artifact dir (0700) if missing. It also
// creates the per-subject cursor dir cc-interact's stream consumers write into.
func EnsureReviewDir(reviewID string) error { return App().EnsureSubjectDir(reviewID) }
