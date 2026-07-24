// Package paths owns cc-review's exact derived-state layout under ~/.cc-review/v1.
// Daemonkit supplies process paths, cc-interact owns its nested database and
// cursor namespace, and this package adds product review artifacts.
package paths

import (
	"fmt"
	"path/filepath"

	ccstore "github.com/yasyf/cc-interact/store"
	ccpaths "github.com/yasyf/daemonkit/paths"
)

const app = ".cc-review/v1"

// App is cc-review's daemonkit state-directory layout.
func App() ccpaths.Paths { return ccpaths.Paths{App: app} }

// StateDir is cc-review's private v1 state directory (~/.cc-review/v1).
func StateDir() string { return App().StateDir() }

// EnsureStateDir creates ~/.cc-review/v1 (0700) if missing.
func EnsureStateDir() error { return App().EnsureStateDir() }

// DBPath is the exact cc-interact v1 database inside cc-review's derived state namespace.
func DBPath() string { return ccstore.Path(App()) }

// ReviewDir is the product-owned on-disk artifact directory for a single review.
func ReviewDir(reviewID string) string { return App().SubjectDir(reviewID) }

// SectionSnapshotPath is the patch file for a section at position pos of
// version v of a review. The JSONL and organization sidecars derive from it via
// the shared TrimSuffix(".patch") pattern.
func SectionSnapshotPath(reviewID string, version, position int) string {
	return filepath.Join(ReviewDir(reviewID), fmt.Sprintf("snap_%d_%d.patch", version, position))
}

// FeedbackPath is the frozen feedback JSON for version v of a review.
func FeedbackPath(reviewID string, version int) string {
	return filepath.Join(ReviewDir(reviewID), fmt.Sprintf("feedback_%d.json", version))
}

// EnsureReviewDir creates a review's artifact dir (0700) if missing. It also
// creates the per-subject cursor dir cc-interact's stream consumers write into.
func EnsureReviewDir(reviewID string) error { return App().EnsureSubjectDir(reviewID) }
