package httpserver

import (
	"net/http"
	"sync"
	"time"

	"coralline/ui"
)

const (
	// revealTTL bounds how long a minted secret waits, in memory, to be
	// shown, and how long a used submission key is remembered.
	revealTTL = 10 * time.Minute
	// maxRevealsPerSession and maxClaimsPerSession bound memory per session.
	maxRevealsPerSession = 5
	maxClaimsPerSession  = 32
	// claimWait is how long a duplicate submission waits for the first one
	// to finish minting before giving up.
	claimWait = 10 * time.Second
)

// claim tracks one create-form submission key.
type claim struct {
	tokenID string
	done    chan struct{} // closed once the first request completes or fails
	closed  bool
	at      time.Time
}

type pendingReveal struct {
	plaintext string
	expires   time.Time
}

type sessionReveals struct {
	claims  map[string]*claim        // submission key → claim
	secrets map[string]pendingReveal // token id → secret awaiting its one view
}

// revealStore implements ui.RevealStore. Everything is keyed by the browser
// session, lives only in memory, expires after revealTTL, and is dropped the
// moment its session ends (sign-out, expiry, eviction) — a secret can only
// ever be shown to the session that minted it, once.
type revealStore struct {
	mu        sync.Mutex
	bySession map[string]*sessionReveals
	now       func() time.Time
}

var _ ui.RevealStore = (*revealStore)(nil)

// newRevealStore returns a store tied to sessions: whenever sessions drops a
// session, its pending secrets and submission keys go with it.
func newRevealStore(sessions *sessionStore) *revealStore {
	rs := &revealStore{bySession: map[string]*sessionReveals{}, now: time.Now}
	if sessions != nil {
		sessions.onDrop = rs.forget
	}
	return rs
}

func requestSessionID(r *http.Request) string {
	return viewerFrom(r.Context()).session.id
}

func (rs *revealStore) Claim(r *http.Request, key string) (string, bool) {
	sid := requestSessionID(r)
	if sid == "" {
		// The admin gate guarantees a session; without one there is nothing
		// to deduplicate against.
		return "", true
	}
	rs.mu.Lock()
	e := rs.entryLocked(sid)
	if c, ok := e.claims[key]; ok {
		rs.mu.Unlock()
		select {
		case <-c.done:
		case <-time.After(claimWait):
		case <-r.Context().Done():
		}
		rs.mu.Lock()
		defer rs.mu.Unlock()
		return c.tokenID, false
	}
	if len(e.claims) >= maxClaimsPerSession {
		rs.dropOldestClaimLocked(e)
	}
	e.claims[key] = &claim{done: make(chan struct{}), at: rs.now()}
	rs.mu.Unlock()
	return "", true
}

func (rs *revealStore) Complete(r *http.Request, key, tokenID, plaintext string) {
	sid := requestSessionID(r)
	if sid == "" {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	e := rs.entryLocked(sid)
	if c, ok := e.claims[key]; ok {
		c.tokenID = tokenID
		closeClaim(c)
	}
	if len(e.secrets) >= maxRevealsPerSession {
		rs.dropOldestSecretLocked(e)
	}
	e.secrets[tokenID] = pendingReveal{plaintext: plaintext, expires: rs.now().Add(revealTTL)}
}

func (rs *revealStore) Abandon(r *http.Request, key string) {
	sid := requestSessionID(r)
	if sid == "" {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if e, ok := rs.bySession[sid]; ok {
		if c, ok := e.claims[key]; ok {
			closeClaim(c)
		}
	}
}

func (rs *revealStore) Take(r *http.Request, tokenID string) (string, bool) {
	sid := requestSessionID(r)
	if sid == "" {
		return "", false
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	e, ok := rs.bySession[sid]
	if !ok {
		return "", false
	}
	secret, ok := e.secrets[tokenID]
	delete(e.secrets, tokenID)
	rs.cleanupLocked(sid, e)
	if !ok || !rs.now().Before(secret.expires) {
		return "", false
	}
	return secret.plaintext, true
}

// forget drops everything held for a session. sessionStore calls it with
// its own lock held; revealStore never calls back into sessionStore, so the
// lock order is always sessions → reveals.
func (rs *revealStore) forget(sessionID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if e, ok := rs.bySession[sessionID]; ok {
		for _, c := range e.claims {
			closeClaim(c)
		}
		delete(rs.bySession, sessionID)
	}
}

// entryLocked returns the session's state, creating it and sweeping out
// anything expired.
func (rs *revealStore) entryLocked(sid string) *sessionReveals {
	e, ok := rs.bySession[sid]
	if !ok {
		e = &sessionReveals{claims: map[string]*claim{}, secrets: map[string]pendingReveal{}}
		rs.bySession[sid] = e
	}
	now := rs.now()
	for id, secret := range e.secrets {
		if !now.Before(secret.expires) {
			delete(e.secrets, id)
		}
	}
	for key, c := range e.claims {
		if now.Sub(c.at) > revealTTL {
			closeClaim(c)
			delete(e.claims, key)
		}
	}
	return e
}

func (rs *revealStore) cleanupLocked(sid string, e *sessionReveals) {
	if len(e.claims) == 0 && len(e.secrets) == 0 {
		delete(rs.bySession, sid)
	}
}

func (rs *revealStore) dropOldestClaimLocked(e *sessionReveals) {
	var oldestKey string
	var oldest time.Time
	for key, c := range e.claims {
		if oldestKey == "" || c.at.Before(oldest) {
			oldestKey, oldest = key, c.at
		}
	}
	if c, ok := e.claims[oldestKey]; ok {
		closeClaim(c)
		delete(e.claims, oldestKey)
	}
}

func (rs *revealStore) dropOldestSecretLocked(e *sessionReveals) {
	var oldestID string
	var oldest time.Time
	for id, secret := range e.secrets {
		if oldestID == "" || secret.expires.Before(oldest) {
			oldestID, oldest = id, secret.expires
		}
	}
	delete(e.secrets, oldestID)
}

func closeClaim(c *claim) {
	if !c.closed {
		c.closed = true
		close(c.done)
	}
}
