// Package thinstore keeps one blobless bare clone per GitHub repository so a
// pull request's diff fetches only the blobs of the files it changes.
package thinstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yasyf/cc-review/internal/github"
	"github.com/yasyf/cc-review/internal/paths"
)

const (
	prRefPrefix  = "refs/cc-review/pr/"
	deepenStart  = 256
	deepenMaxCap = 4096
	staleLockAge = 10 * time.Minute
	gitStopGrace = 10 * time.Second
)

var (
	cloneConfig = []string{"feature.manyFiles=true", "pack.threads=2"}
	locks       sync.Map
)

// Store is a blobless bare clone under paths.Repos().
type Store struct {
	Dir string
}

// Open clones repo from cloneURL into its store when absent and verifies the
// store still fetches blobs lazily from it.
func Open(ctx context.Context, repo github.Repo, cloneURL string) (*Store, error) {
	s := &Store{Dir: filepath.Join(paths.Repos(), repo.Owner, repo.Name+".git")}
	defer s.lock()()
	switch _, err := os.Stat(s.Dir); {
	case errors.Is(err, fs.ErrNotExist):
		if err := clone(ctx, s.Dir, cloneURL); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("thinstore: stat %s: %w", s.Dir, err)
	}
	if err := verify(ctx, s.Dir, cloneURL); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) lock() func() {
	mu, _ := locks.LoadOrStore(s.Dir, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

func clone(ctx context.Context, dir, cloneURL string) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("thinstore: %w", err)
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+"-")
	if err != nil {
		return fmt.Errorf("thinstore: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	staged := filepath.Join(stage, filepath.Base(dir))
	argv := []string{"clone", "--bare", "--no-local", "--filter=blob:none", "--depth=1", "--no-tags", "--single-branch"}
	for _, kv := range cloneConfig {
		argv = append(argv, "-c", kv)
	}
	if _, err := git(ctx, stage, nil, append(argv, cloneURL, staged)...); err != nil {
		return fmt.Errorf("thinstore: clone %s: %w", cloneURL, err)
	}
	if err := os.Rename(staged, dir); err != nil {
		return fmt.Errorf("thinstore: install the store at %s: %w", dir, err)
	}
	return nil
}

func verify(ctx context.Context, dir, cloneURL string) error {
	out, err := git(ctx, dir, nil, "config", "--get-regexp", `^remote\.origin\.`)
	if err != nil {
		return fmt.Errorf("thinstore: read %s's origin: %w", dir, err)
	}
	got := map[string][]string{}
	for line := range strings.Lines(out) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		key = strings.TrimPrefix(key, "remote.origin.")
		got[key] = append(got[key], value)
	}
	want := map[string][]string{
		"url":                {cloneURL},
		"promisor":           {"true"},
		"partialclonefilter": {"blob:none"},
		"tagopt":             {"--no-tags"},
	}
	for _, key := range slices.Sorted(maps.Keys(want)) {
		if !slices.Equal(got[key], want[key]) {
			return fmt.Errorf("thinstore: %s is not a blobless store of %s: remote.origin.%s is %q, want %q", dir, cloneURL, key, got[key], want[key])
		}
	}
	return nil
}

// FetchPRs points refs/cc-review/pr/<number> at each pull request's head,
// fetching every head the store lacks in one round trip.
func (s *Store) FetchPRs(ctx context.Context, prs ...github.PullRequest) error {
	defer s.lock()()
	heads := make([]string, len(prs))
	for i, pr := range prs {
		heads[i] = pr.HeadRefOid
	}
	absent, err := s.missing(ctx, heads...)
	if err != nil {
		return err
	}
	var refspecs []string
	var updates strings.Builder
	for _, pr := range prs {
		if slices.Contains(absent, pr.HeadRefOid) {
			refspecs = append(refspecs, prRefspec(pr.Number))
			continue
		}
		fmt.Fprintf(&updates, "update %s%d %s\n", prRefPrefix, pr.Number, pr.HeadRefOid)
	}
	if updates.Len() > 0 {
		if _, err := git(ctx, s.Dir, strings.NewReader(updates.String()), "update-ref", "--stdin"); err != nil {
			return fmt.Errorf("thinstore: point pull request refs in %s: %w", s.Dir, err)
		}
	}
	if len(refspecs) == 0 {
		return nil
	}
	if err := s.fetch(ctx, "--depth=1", refspecs...); err != nil {
		return fmt.Errorf("thinstore: fetch %v into %s: %w", refspecs, s.Dir, err)
	}
	return nil
}

// FetchCommits makes every sha present in the store, fetching the absent ones
// in one round trip. When the server refuses a want by sha, it deepens every
// fetched pull request head until each arrives, at most 4096 commits.
func (s *Store) FetchCommits(ctx context.Context, shas ...string) error {
	defer s.lock()()
	absent, err := s.missing(ctx, shas...)
	if err != nil || len(absent) == 0 {
		return err
	}
	fetchErr := s.fetch(ctx, "--depth=1", absent...)
	if fetchErr == nil {
		return nil
	}
	for _, sha := range absent {
		if err := s.deepenTo(ctx, sha); err != nil {
			return fmt.Errorf("thinstore: fetch %s into %s: %w", sha, s.Dir, errors.Join(fetchErr, err))
		}
	}
	return nil
}

func (s *Store) deepenTo(ctx context.Context, sha string) error {
	out, err := git(ctx, s.Dir, nil, "for-each-ref", "--format=%(refname)", prRefPrefix)
	if err != nil {
		return err
	}
	var refspecs []string
	for ref := range strings.FieldsSeq(out) {
		number, err := strconv.Atoi(strings.TrimPrefix(ref, prRefPrefix))
		if err != nil {
			return fmt.Errorf("%s is not a pull request ref: %w", ref, err)
		}
		refspecs = append(refspecs, prRefspec(number))
	}
	if len(refspecs) == 0 {
		return errors.New("no pull request head to deepen")
	}
	for total, step := 0, deepenStart; total < deepenMaxCap; step *= 2 {
		step = min(step, deepenMaxCap-total)
		if err := s.fetch(ctx, "--deepen="+strconv.Itoa(step), refspecs...); err != nil {
			return err
		}
		total += step
		absent, err := s.missing(ctx, sha)
		if err != nil || len(absent) == 0 {
			return err
		}
	}
	return fmt.Errorf("%s is not within %d commits of any fetched pull request head", sha, deepenMaxCap)
}

func (s *Store) fetch(ctx context.Context, depth string, refspecs ...string) error {
	if err := clearStaleLock(filepath.Join(s.Dir, "shallow.lock")); err != nil {
		return err
	}
	argv := append([]string{"fetch", "--no-tags", "--no-write-fetch-head", "--filter=blob:none", depth, "origin"}, refspecs...)
	_, err := git(ctx, s.Dir, nil, argv...)
	return err
}

// clearStaleLock removes a shallow.lock a killed fetch left behind. The caller
// holds the store's lock, so no fetch of this daemon is running; one older than
// any fetch can take belongs to nobody.
func clearStaleLock(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && time.Since(info.ModTime()) < staleLockAge {
		return nil
	}
	if err != nil {
		return fmt.Errorf("thinstore: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("thinstore: remove stale %s: %w", path, err)
	}
	return nil
}

// missing returns the shas whose commits the store lacks, in one cat-file run
// that never fetches.
func (s *Store) missing(ctx context.Context, shas ...string) ([]string, error) {
	var query strings.Builder
	for _, sha := range shas {
		query.WriteString(sha + "^{commit}\n")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", s.Dir, "cat-file", "--batch-check=%(objectname)") //nolint:gosec // G204: fixed git subcommand against the store.
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	cmd.Stdin = strings.NewReader(query.String())
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("thinstore: check %d commits in %s: %w", len(shas), s.Dir, err)
	}
	var absent []string
	for i, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if strings.HasSuffix(line, " missing") {
			absent = append(absent, shas[i])
		}
	}
	return absent, nil
}

func prRefspec(number int) string {
	n := strconv.Itoa(number)
	return "+refs/pull/" + n + "/head:" + prRefPrefix + n
}

func git(ctx context.Context, dir string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: fixed git binary; args are this package's own argv.
	cmd.Stdin = stdin
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = gitStopGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
