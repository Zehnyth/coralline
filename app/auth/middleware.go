package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"coralline/app/domain"
)

type contextKey string

const tokenContextKey contextKey = "coralline_token"

// argonQueueWait is how long a request waits for an argon2id slot before
// getting 429. One verification takes tens of milliseconds, so this absorbs
// a terraform init burst without letting a flood queue indefinitely.
const argonQueueWait = time.Second

// TokenLookup is the minimal interface the middleware needs from the
// repository — kept narrow so this package doesn't import sqlitestore
// directly (avoids an import cycle and keeps auth independently
// testable against a fake).
type TokenLookup interface {
	GetToken(ctx context.Context, id string) (*domain.Token, error)
}

// LastUsedRecorder receives a token id each time it authenticates. It must
// not block — see sqlitestore.LastUsedRecorder.
type LastUsedRecorder interface {
	Record(tokenID string, at time.Time)
}

type Verifier struct {
	Store          TokenLookup
	Semaphore      *ArgonSemaphore
	Limiter        *IPRateLimiter
	TrustedProxies map[string]bool
	LastUsed       LastUsedRecorder // optional
}

// Authenticate verifies the Authorization: Bearer <token> header.
func (v *Verifier) Authenticate(r *http.Request) (*domain.Token, error) {
	authz := r.Header.Get("Authorization")
	presented := strings.TrimPrefix(authz, "Bearer ")
	if presented == "" || presented == authz {
		return nil, domain.ErrUnauthorized
	}
	return v.AuthenticateToken(r, presented)
}

// AuthenticateToken verifies a presented token string — from a bearer
// header, or pasted into the web UI's sign-in form — through the exact same
// checks. It never runs a dummy argon2id hash for an unknown token id (PRD
// FR-4.9) — the timing asymmetry is accepted given 128-bit id entropy, and
// equalizing it would hand attackers a free CPU-exhaustion vector.
func (v *Verifier) AuthenticateToken(r *http.Request, presented string) (*domain.Token, error) {
	id, secret, ok := ParseToken(strings.TrimSpace(presented))
	if !ok {
		return nil, domain.ErrUnauthorized
	}

	ip := ClientIP(r, v.TrustedProxies)
	if v.Limiter != nil && !v.Limiter.Allow(ip) {
		return nil, domain.ErrRateLimited
	}
	// Only failures count toward the per-IP limit; see IPRateLimiter.Allow.
	reject := func() (*domain.Token, error) {
		if v.Limiter != nil {
			v.Limiter.RecordFailure(ip)
		}
		return nil, domain.ErrUnauthorized
	}

	tok, err := v.Store.GetToken(r.Context(), id)
	if err != nil {
		return reject() // no dummy hash on unknown id
	}

	if !v.Semaphore.Acquire(r.Context(), argonQueueWait) {
		return nil, domain.ErrRateLimited
	}
	defer v.Semaphore.Release()

	if !VerifySecret(secret, tok.SecretHash) {
		return reject()
	}
	now := time.Now()
	if !tok.Active(now) {
		return reject()
	}
	if v.LastUsed != nil {
		v.LastUsed.Record(tok.ID, now)
	}
	return tok, nil
}

// RequireScope wraps an http.Handler, authenticating the request and
// checking the given scope check function (which receives the request
// so it can inspect path values like the namespace). anonymousOK, when
// true, bypasses authentication entirely — set only when the operator has
// turned require_login off (PRD FR-1.7).
func (v *Verifier) RequireScope(anonymousOK bool, check func(r *http.Request, t *domain.Token) bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if anonymousOK {
			next(w, r)
			return
		}
		tok, err := v.Authenticate(r)
		if err != nil {
			if errors.Is(err, domain.ErrRateLimited) {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if check != nil && !check(r, tok) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), tokenContextKey, tok)
		next(w, r.WithContext(ctx))
	}
}

func TokenFromContext(ctx context.Context) *domain.Token {
	t, _ := ctx.Value(tokenContextKey).(*domain.Token)
	return t
}
