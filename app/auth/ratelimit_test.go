package auth

import (
	"context"
	"testing"
	"time"
)

func TestIPRateLimiterCountsOnlyFailures(t *testing.T) {
	l := NewIPRateLimiter(3, time.Minute, 10)

	// Any number of checks without failures stays allowed.
	for i := 0; i < 25; i++ {
		if !l.Allow("10.0.0.1") {
			t.Fatalf("check %d refused with no failures recorded", i)
		}
	}

	for i := 0; i < 3; i++ {
		l.RecordFailure("10.0.0.1")
	}
	if l.Allow("10.0.0.1") {
		t.Error("allowed after reaching the failure limit")
	}
	if !l.Allow("10.0.0.2") {
		t.Error("another address was throttled")
	}
}

func TestArgonSemaphoreAcquireWaitsForASlot(t *testing.T) {
	s := NewArgonSemaphore(1)
	if !s.TryAcquire() {
		t.Fatal("first acquire failed")
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.Release()
	}()
	if !s.Acquire(context.Background(), time.Second) {
		t.Fatal("Acquire did not get the released slot")
	}
	if s.Acquire(context.Background(), 10*time.Millisecond) {
		t.Fatal("Acquire succeeded while saturated")
	}
}
