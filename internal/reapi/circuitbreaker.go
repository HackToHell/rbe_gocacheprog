package reapi

import (
	"sync"
	"time"
)

// CircuitBreaker tracks consecutive failures and trips open after a threshold.
// When open, calls fail fast for a cooldown period before allowing a probe.
type CircuitBreaker struct {
	mu          sync.Mutex
	failures    int
	threshold   int
	lastFailure time.Time
	cooldown    time.Duration
	tripped     bool
}

// NewCircuitBreaker creates a circuit breaker that opens after `threshold`
// consecutive failures and stays open for `cooldown` duration.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
	}
}

// Allow returns true if the request should be allowed through.
// When the circuit is open, it allows one probe request after the cooldown.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if !cb.tripped {
		return true
	}

	// Allow a probe after cooldown
	if time.Since(cb.lastFailure) >= cb.cooldown {
		return true
	}

	return false
}

// RecordSuccess resets the failure count and closes the circuit.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.tripped = false
}

// RecordFailure increments the failure count and potentially trips the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.failures >= cb.threshold {
		cb.tripped = true
	}
}

// IsOpen returns whether the circuit is currently open (tripped).
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.tripped
}
