// Package corrections feeds cc-transcript's shared corrections ledger from a
// frozen review: one anchored code-correction per inline comment thread. Unlike
// the decision ledger (which cc-review writes as SQLite directly, see
// internal/decisions), the corrections ledger is owned by cc-transcript — its
// location and schema are not cc-review's to assume — so each thread is appended
// by shelling out to `cc-transcript corrections add`, which is idempotent on
// (session, anchor). Human review rows carry a null digest; the anchor
// review:<reviewID>:<commentID> is their identity.
package corrections

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/yasyf/cc-review/internal/feedback"
)

// binEnv overrides the cc-transcript binary; production resolves the default off
// PATH (installed as `uvx cc-transcript`), tests point it at a dev build.
const binEnv = "CC_TRANSCRIPT_BIN"

const defaultBin = "cc-transcript"

// Bin is the cc-transcript executable to invoke, taken from CC_TRANSCRIPT_BIN or
// defaulting to `cc-transcript` on PATH.
func Bin() string {
	if bin := os.Getenv(binEnv); bin != "" {
		return bin
	}
	return defaultBin
}

// args builds the `cc-transcript corrections add` argv for one frozen thread:
// a human review row keyed by its anchor, with a null digest. ts is the submit
// time stamped onto every thread of the same submission.
func args(reviewID string, t feedback.Thread, sessionID, repoKey string, ts time.Time) []string {
	return []string{
		"corrections", "add",
		"--session", sessionID,
		"--source", "cc-review",
		"--anchor", fmt.Sprintf("review:%s:%d", reviewID, t.CommentID),
		"--origin", "review",
		"--incorrect-file", t.FilePath,
		"--incorrect-new", t.LineContent,
		"--correction-text", t.Body,
		"--repo", repoKey,
		"--ts-ms", strconv.FormatInt(ts.UnixMilli(), 10),
	}
}

// Write appends every thread of a frozen review to the corrections ledger as one
// `cc-transcript corrections add` per thread, stamped with the review's session,
// the repo key, and the submit time. Per-thread failures are collected and
// returned joined, so one bad row never strands the rest. A review with no
// session UUID cannot anchor its corrections to a session, so it is an error.
func Write(ctx context.Context, fb feedback.Feedback, repoKey string, submittedAt time.Time) error {
	if fb.SessionID == "" {
		return fmt.Errorf("write corrections for review %s: no session id on frozen feedback", fb.ReviewID)
	}
	bin := Bin()
	var errs []error
	for _, t := range fb.Threads {
		cmd := exec.CommandContext(ctx, bin, args(fb.ReviewID, t, fb.SessionID, repoKey, submittedAt)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Errorf("add correction for comment %d: %w: %s", t.CommentID, err, out))
		}
	}
	return errors.Join(errs...)
}
