package reapi_test

import (
	"testing"
	"time"

	"github.com/hacktohell/gocache-rbe/internal/reapi"
)

func TestCircuitBreakerFullLifecycle(t *testing.T) {
	// closed -> record failures -> open -> cooldown -> half-open (probe allowed) -> success -> closed
	cb := reapi.NewCircuitBreaker(3, 50*time.Millisecond)

	// Initially closed
	if cb.IsOpen() {
		t.Fatal("new circuit breaker should be closed")
	}
	if !cb.Allow() {
		t.Fatal("closed circuit breaker should allow requests")
	}

	// Record failures below threshold - still closed
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsOpen() {
		t.Fatal("should still be closed after 2 failures (threshold=3)")
	}
	if !cb.Allow() {
		t.Fatal("should still allow after 2 failures")
	}

	// Third failure trips the circuit
	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("should be open after 3 failures")
	}
	if cb.Allow() {
		t.Fatal("should not allow requests when open and within cooldown")
	}

	// Wait for cooldown to elapse
	time.Sleep(60 * time.Millisecond)

	// Half-open: probe should be allowed
	if !cb.Allow() {
		t.Fatal("should allow probe after cooldown")
	}
	// Circuit is still technically tripped
	if !cb.IsOpen() {
		t.Fatal("should still report open until success recorded")
	}

	// Record success - closes the circuit
	cb.RecordSuccess()
	if cb.IsOpen() {
		t.Fatal("should be closed after success")
	}
	if !cb.Allow() {
		t.Fatal("should allow after success reset")
	}
}

func TestCircuitBreakerSuccessResetsFailures(t *testing.T) {
	cb := reapi.NewCircuitBreaker(3, time.Minute)

	cb.RecordFailure()
	cb.RecordFailure()
	// Two failures, then success resets the count
	cb.RecordSuccess()

	// Should need 3 more failures to trip
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsOpen() {
		t.Fatal("should not be open after reset + 2 failures")
	}

	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("should be open after 3 consecutive failures post-reset")
	}
}

func TestCircuitBreakerThresholdOne(t *testing.T) {
	cb := reapi.NewCircuitBreaker(1, 50*time.Millisecond)

	if cb.IsOpen() {
		t.Fatal("should start closed")
	}

	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("threshold=1 should trip after one failure")
	}
	if cb.Allow() {
		t.Fatal("should block within cooldown")
	}

	time.Sleep(60 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("should allow probe after cooldown")
	}
}

func TestCircuitBreakerStaysOpenDuringCooldown(t *testing.T) {
	cb := reapi.NewCircuitBreaker(1, time.Minute)

	cb.RecordFailure()
	// Multiple checks during cooldown should all be blocked
	for range 5 {
		if cb.Allow() {
			t.Fatal("should not allow during cooldown")
		}
	}
}
