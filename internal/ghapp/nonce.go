package ghapp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type nonces[T any] struct {
	mu     sync.Mutex
	ttl    time.Duration
	issued map[string]nonce[T]
}

type nonce[T any] struct {
	value T
	at    time.Time
}

func newNonces[T any](ttl time.Duration) *nonces[T] {
	return &nonces[T]{ttl: ttl, issued: map[string]nonce[T]{}}
}

func (n *nonces[T]) issue(value T) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	key := hex.EncodeToString(b)
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, e := range n.issued {
		if now().Sub(e.at) > n.ttl {
			delete(n.issued, k)
		}
	}
	n.issued[key] = nonce[T]{value: value, at: now()}
	return key
}

func (n *nonces[T]) lookup(key string) (T, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.issued[key]
	if !ok || now().Sub(e.at) > n.ttl {
		var zero T
		return zero, false
	}
	return e.value, true
}

func (n *nonces[T]) consume(key string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.issued[key]
	delete(n.issued, key)
	return ok && now().Sub(e.at) <= n.ttl
}
