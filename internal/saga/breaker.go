package saga

import (
	"sync"
	"time"
)

// breakerSet holds an independent circuit breaker per service id, so one
// failing provider does not affect calls to others.
//
// Each breaker is a simple consecutive-failure breaker:
//   - closed: calls allowed; `threshold` consecutive failures -> open.
//   - open:   calls rejected until `cooldown` elapses -> half-open.
//   - half-open: one trial call allowed; success -> closed, failure -> open.
type breakerSet struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	states    map[string]*breakerState
}

type breakerState struct {
	failures int
	open     bool
	openedAt time.Time
	halfOpen bool
}

func newBreakerSet(threshold int, cooldown time.Duration) *breakerSet {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &breakerSet{
		threshold: threshold,
		cooldown:  cooldown,
		states:    map[string]*breakerState{},
	}
}

// allow reports whether a call to serviceID may proceed at time now. It also
// transitions an open breaker to half-open once the cooldown has elapsed.
func (b *breakerSet) allow(serviceID string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(serviceID)
	if !s.open {
		return true
	}
	if now.Sub(s.openedAt) >= b.cooldown {
		// Cooldown elapsed: allow a single trial (half-open).
		s.halfOpen = true
		return true
	}
	return false
}

// record updates the breaker after a call. success closes/keeps-closed it;
// failure increments and may open it.
func (b *breakerSet) record(serviceID string, now time.Time, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(serviceID)
	if success {
		s.failures = 0
		s.open = false
		s.halfOpen = false
		return
	}
	// Failure.
	if s.halfOpen {
		// Trial failed: re-open immediately.
		s.open = true
		s.openedAt = now
		s.halfOpen = false
		return
	}
	s.failures++
	if s.failures >= b.threshold {
		s.open = true
		s.openedAt = now
	}
}

func (b *breakerSet) get(serviceID string) *breakerState {
	s, ok := b.states[serviceID]
	if !ok {
		s = &breakerState{}
		b.states[serviceID] = s
	}
	return s
}
