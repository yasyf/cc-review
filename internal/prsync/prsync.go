// Package prsync mirrors a PR review's GitHub threads, comments, and metadata
// into the local store, one poller per open kind=pr review.
package prsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	ccd "github.com/yasyf/cc-interact/daemon"
	"golang.org/x/sync/singleflight"

	"github.com/yasyf/cc-review/internal/ghapp"
	"github.com/yasyf/cc-review/internal/github"
)

var (
	watchedInterval = time.Minute
	idleInterval    = 5 * time.Minute
)

// ErrAppMissing refuses a poller before the cc-review GitHub App exists: without
// its bot login, Claude's own replies would sync back as a coworker's.
var ErrAppMissing = errors.New("the cc-review GitHub App is not set up; run `cc-review github setup`")

// Config wires a Syncer to the daemon. Watched reports whether a review has a
// live viewer; Recapture mints a new version from the PRs' current heads;
// Paused reports whether every poller should skip its queries;
// Background runs daemon-lifecycle work (ccd.Server.Background).
type Config struct {
	DB         func() *sql.DB
	Append     ccd.AppendFunc
	Client     *github.Client
	Watched    func(reviewID string) bool
	Recapture  func(ctx context.Context, reviewID string) error
	Paused     func() bool
	Background func(fn func(context.Context))
	Log        *log.Logger
}

// Syncer owns the per-review pollers. Reviews of the same pull requests share
// one GitHub snapshot per poll interval, so a second review adds no queries.
type Syncer struct {
	cfg Config

	mu      sync.Mutex
	viewer  string
	pollers map[string]*poller
	applies map[string]*applyGate

	snapMu   sync.Mutex
	snaps    map[string]sharedSnapshot
	writes   atomic.Uint64
	fetching singleflight.Group
}

type sharedSnapshot struct {
	at     time.Time
	writes uint64
	snaps  map[int]github.PRSnapshot
}

type applyGate struct {
	mu     sync.Mutex
	writes atomic.Uint64
}

type poller struct {
	reviewID string
	maxAge   time.Duration
	app      ghapp.App
	viewer   string
	cancel   context.CancelFunc
	done     chan struct{}
}

// New builds a Syncer over cfg; no poller runs until Start.
func New(cfg Config) *Syncer {
	return &Syncer{cfg: cfg, pollers: make(map[string]*poller), applies: make(map[string]*applyGate), snaps: make(map[string]sharedSnapshot)}
}

// Exclusive holds off the review's poller from applying a snapshot until
// release. An outbound write holds it from the GitHub call through storing the
// result; release marks every snapshot fetched before it stale, so the poller
// refetches instead of reverting the write.
func (s *Syncer) Exclusive(reviewID string) (release func()) {
	g := s.gate(reviewID)
	g.mu.Lock()
	return func() {
		s.writes.Add(1)
		g.writes.Add(1)
		g.mu.Unlock()
	}
}

func (s *Syncer) gate(reviewID string) *applyGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.applies[reviewID]
	if !ok {
		g = &applyGate{}
		s.applies[reviewID] = g
	}
	return g
}

// Start runs the review's poller unless one is already running. It refuses with
// ErrAppMissing until the GitHub App is set up, and resolves the viewer's login
// once per daemon.
func (s *Syncer) Start(ctx context.Context, reviewID string) error {
	app, ok, err := ghapp.Load()
	if err != nil {
		return fmt.Errorf("load github app: %w", err)
	}
	if !ok {
		return ErrAppMissing
	}
	viewer, err := s.viewerLogin(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.pollers[reviewID]; running {
		return nil
	}
	pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p := &poller{reviewID: reviewID, app: app, viewer: viewer, cancel: cancel, done: make(chan struct{})}
	s.pollers[reviewID] = p
	s.cfg.Background(func(daemonCtx context.Context) {
		stop := context.AfterFunc(daemonCtx, cancel)
		defer stop()
		defer s.forget(p)
		s.run(pctx, p)
	})
	return nil
}

// Stop cancels the review's poller and waits for it to exit.
func (s *Syncer) Stop(reviewID string) {
	s.mu.Lock()
	p, ok := s.pollers[reviewID]
	s.mu.Unlock()
	if !ok {
		return
	}
	p.cancel()
	<-p.done
}

func (s *Syncer) running(reviewID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pollers[reviewID]
	return ok
}

func (s *Syncer) viewerLogin(ctx context.Context) (string, error) {
	s.mu.Lock()
	viewer := s.viewer
	s.mu.Unlock()
	if viewer != "" {
		return viewer, nil
	}
	viewer, err := s.cfg.Client.Viewer(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve github viewer: %w", err)
	}
	s.mu.Lock()
	s.viewer = viewer
	s.mu.Unlock()
	return viewer, nil
}

func (s *Syncer) forget(p *poller) {
	s.mu.Lock()
	if s.pollers[p.reviewID] == p {
		delete(s.pollers, p.reviewID)
	}
	s.mu.Unlock()
	p.cancel()
	close(p.done)
}

func (s *Syncer) run(ctx context.Context, p *poller) {
	t := time.NewTicker(watchedInterval)
	defer t.Stop()
	var last time.Time
	limited := false
	for {
		watched := s.cfg.Watched(p.reviewID) && !limited
		if !s.cfg.Paused() && (last.IsZero() || watched || time.Since(last) >= idleInterval) {
			p.maxAge = idleInterval
			if watched {
				p.maxAge = watchedInterval
			}
			live, err := s.poll(ctx, p)
			last = time.Now()
			limited = errors.Is(err, github.ErrRateLimited)
			if err != nil && ctx.Err() == nil {
				s.cfg.Log.Printf("prsync %s: %v", p.reviewID, err)
			}
			if !live {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
