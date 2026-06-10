package daemon

import (
	"sync"
	"time"
)

// Activity tracks which stream consumers are wired to a review: live SSE
// attachments (per review) and recent resolve polls (per repo root). It is how
// handleStart can report a channel consumer's presence without blocking on one.
type Activity struct {
	mu       sync.Mutex
	attached map[string]map[string]int // reviewID → consumer → open SSE conns
	polls    map[string]time.Time      // repoRoot + "\x00" + consumer → last poll
	now      func() time.Time
}

// NewActivity returns an empty registry.
func NewActivity() *Activity {
	return &Activity{
		attached: make(map[string]map[string]int),
		polls:    make(map[string]time.Time),
		now:      time.Now,
	}
}

// Attach records one open SSE connection for a named consumer and returns its
// detach. Counting (not a flag) keeps an overlapping reconnect attached.
func (a *Activity) Attach(reviewID, consumer string) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.attached[reviewID]
	if m == nil {
		m = make(map[string]int)
		a.attached[reviewID] = m
	}
	m[consumer]++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if m[consumer]--; m[consumer] <= 0 {
				delete(m, consumer)
			}
		})
	}
}

// Attached reports whether the consumer has an open SSE connection to the review.
func (a *Activity) Attached(reviewID, consumer string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.attached[reviewID][consumer] > 0
}

// NotePoll records that the consumer just polled resolve for this repo root.
func (a *Activity) NotePoll(repoRoot, consumer string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.polls[repoRoot+"\x00"+consumer] = a.now()
}

// PolledSince reports whether the consumer polled for this repo root within window.
func (a *Activity) PolledSince(repoRoot, consumer string, window time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.polls[repoRoot+"\x00"+consumer]
	return ok && a.now().Sub(t) <= window
}
