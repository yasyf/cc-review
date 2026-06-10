package daemon

import (
	"strconv"
	"sync"
	"time"
)

// Activity tracks which stream consumers are wired to a review: live SSE
// attachments (per review, keyed by consumer name + window pid) and recent
// resolve polls (per repo root, same key). It is how handleStart can report a
// channel consumer's presence without blocking on one, and how held sees a
// pid-less review's recent attachment.
type Activity struct {
	mu       sync.Mutex
	attached map[string]map[attachKey]int
	lastDrop map[string]time.Time
	polls    map[string]time.Time
	now      func() time.Time
}

type attachKey struct {
	consumer string
	pid      int
}

// NewActivity returns an empty registry.
func NewActivity() *Activity {
	return &Activity{
		attached: make(map[string]map[attachKey]int),
		lastDrop: make(map[string]time.Time),
		polls:    make(map[string]time.Time),
		now:      time.Now,
	}
}

// Attach records one open SSE connection for a consumer in a window and
// returns its detach. Counting (not a flag) keeps an overlapping reconnect
// attached; the review's last detach stamps lastDrop for AttachedWithin.
func (a *Activity) Attach(reviewID, consumer string, pid int) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.attached[reviewID]
	if m == nil {
		m = make(map[attachKey]int)
		a.attached[reviewID] = m
	}
	k := attachKey{consumer, pid}
	m[k]++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if m[k]--; m[k] <= 0 {
				delete(m, k)
			}
			if len(m) == 0 {
				a.lastDrop[reviewID] = a.now()
			}
		})
	}
}

// Attached reports whether the consumer in that window has an open SSE
// connection to the review.
func (a *Activity) Attached(reviewID, consumer string, pid int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.attached[reviewID][attachKey{consumer, pid}] > 0
}

// AttachedWithin reports whether any consumer is attached to the review now,
// or the review's last attachment dropped within grace of now.
func (a *Activity) AttachedWithin(reviewID string, grace time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.attached[reviewID]) > 0 {
		return true
	}
	t, ok := a.lastDrop[reviewID]
	return ok && a.now().Sub(t) <= grace
}

// NotePoll records that the consumer in that window just polled resolve for
// this repo root.
func (a *Activity) NotePoll(repoRoot, consumer string, pid int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.polls[pollKey(repoRoot, consumer, pid)] = a.now()
}

// PolledSince reports whether the consumer in that window polled for this
// repo root within window.
func (a *Activity) PolledSince(repoRoot, consumer string, pid int, window time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.polls[pollKey(repoRoot, consumer, pid)]
	return ok && a.now().Sub(t) <= window
}

func pollKey(repoRoot, consumer string, pid int) string {
	return repoRoot + "\x00" + consumer + "\x00" + strconv.Itoa(pid)
}
