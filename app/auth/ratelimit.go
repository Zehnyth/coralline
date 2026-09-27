package auth

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// ArgonSemaphore caps the number of concurrent argon2id verifications
// process-wide (PRD SR-3.3). Argon2id is deliberately expensive, so a
// handful of concurrent bogus-secret requests can otherwise starve
// legitimate terraform init traffic — a resource-exhaustion attack that
// needs no brute-force volume at all. Callers that fail to acquire
// should respond 429 before ever calling VerifySecret.
type ArgonSemaphore struct {
	slots chan struct{}
}

func NewArgonSemaphore(maxConcurrent int) *ArgonSemaphore {
	return &ArgonSemaphore{slots: make(chan struct{}, maxConcurrent)}
}

// TryAcquire is non-blocking: returns false immediately if saturated.
func (s *ArgonSemaphore) TryAcquire() bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Acquire waits up to wait for a slot, so a normal burst of parallel
// terraform init requests queues briefly instead of being refused, while a
// sustained flood still gets turned away once the wait runs out.
func (s *ArgonSemaphore) Acquire(ctx context.Context, wait time.Duration) bool {
	if s.TryAcquire() {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case s.slots <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (s *ArgonSemaphore) Release() {
	<-s.slots
}

// IPRateLimiter throttles failed authentication attempts per source
// address, backed by a bounded map so rotating through spoofed or
// botnet addresses cannot exhaust server memory (PRD SR-3.4). This is a
// simple fixed-window counter — adequate for a single-binary tool; it
// is not a substitute for a distributed limiter at very large scale,
// which is deliberately out of scope (no Redis dependency, PRD
// principle: no major dependencies).
type IPRateLimiter struct {
	mu         sync.Mutex
	counts     map[string]*window
	maxEntries int
	limit      int
	period     time.Duration
}

type window struct {
	count   int
	resetAt time.Time
}

func NewIPRateLimiter(limit int, period time.Duration, maxEntries int) *IPRateLimiter {
	return &IPRateLimiter{
		counts:     make(map[string]*window),
		maxEntries: maxEntries,
		limit:      limit,
		period:     period,
	}
}

// Allow reports whether key is still under its failed-attempt limit. It
// does not count the attempt: successful authentications must never use up
// the budget, or a busy CI runner or a whole office behind one NAT address
// would be throttled for doing nothing wrong.
func (l *IPRateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.counts[key]
	if !ok || time.Now().After(w.resetAt) {
		return true
	}
	return w.count < l.limit
}

// RecordFailure counts one failed authentication attempt against key.
func (l *IPRateLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, ok := l.counts[key]
	if !ok || now.After(w.resetAt) {
		if !ok && len(l.counts) >= l.maxEntries {
			l.evictOldest()
		}
		w = &window{count: 0, resetAt: now.Add(l.period)}
		l.counts[key] = w
	}
	w.count++
}

func (l *IPRateLimiter) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, w := range l.counts {
		if first || w.resetAt.Before(oldestTime) {
			oldestKey, oldestTime, first = k, w.resetAt, false
		}
	}
	if oldestKey != "" {
		delete(l.counts, oldestKey)
	}
}

// ClientIP resolves the source address for rate-limiting purposes.
// X-Forwarded-For is honoured ONLY when the direct peer is in
// trustedProxies (PRD SR-3.5) — trusting it unconditionally makes
// per-source limiting trivially spoofable, while ignoring it behind a
// real reverse proxy bans the proxy for every user at once. Both are
// self-inflicted denial of service.
func ClientIP(r *http.Request, trustedProxies map[string]bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if trustedProxies[host] {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := splitAndTrim(fwd, ',')
			if len(parts) > 0 {
				return parts[0]
			}
		}
	}
	return host
}

func splitAndTrim(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	out = append(out, trimSpace(s[start:]))
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
