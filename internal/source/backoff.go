package source

import (
	"sync"
	"time"
)

// Backoff tracks bounded reconnect delays independently for each source. It
// schedules health/reattach work only; user queries are never retried.
type Backoff struct {
	mu       sync.Mutex
	base     time.Duration
	maximum  time.Duration
	attempts map[string]uint
}

func NewBackoff(base, maximum time.Duration) *Backoff {
	if base <= 0 {
		base = 250 * time.Millisecond
	}
	if maximum < base {
		maximum = 30 * time.Second
	}
	return &Backoff{base: base, maximum: maximum, attempts: make(map[string]uint)}
}

// Failure records one failed health/attach attempt and returns the delay before
// that source may be checked again.
func (b *Backoff) Failure(sourceID string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	attempt := b.attempts[sourceID]
	if attempt < 63 {
		b.attempts[sourceID] = attempt + 1
	}
	delay := b.base
	for range attempt {
		if delay >= b.maximum/2 {
			return b.maximum
		}
		delay *= 2
	}
	if delay > b.maximum {
		return b.maximum
	}
	return delay
}

// Ready clears one source without affecting the retry schedule of other
// sources.
func (b *Backoff) Ready(sourceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.attempts, sourceID)
}
