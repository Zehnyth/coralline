package auth

import (
	"context"
	"fmt"
	"time"

	"coralline/app/domain"
)

// NamespaceChecker is the slice of the repository grant validation needs.
type NamespaceChecker interface {
	NamespaceExists(ctx context.Context, name string) (bool, error)
}

// TokenStore is what minting needs from the repository.
type TokenStore interface {
	NamespaceChecker
	CreateToken(ctx context.Context, t domain.Token) error
}

// MaxTokenLifetime bounds expiry so an absurd value is rejected rather
// than overflowing a time.Duration.
const MaxTokenLifetime = 10 * 365 * 24 * time.Hour

// MintRequest is unvalidated input from any transport.
type MintRequest struct {
	Kind domain.TokenKind
	Name string
	// CreatedBy must be set by the caller from the authenticated admin
	// token — never from request input.
	CreatedBy string
	Grants    []domain.GrantInput
	Tags      []domain.Tag
	ExpiresIn time.Duration
}

// MintToken is the one path by which tokens are created. The admin JSON API,
// the web UI, and first-boot bootstrap all come through here, so validation
// cannot drift between surfaces. Which kinds a surface may mint is decided
// by its caller: the public web UI only ever passes TokenKindStandard.
//
// The returned token has its SecretHash cleared; the plaintext is returned
// separately and exists nowhere else.
func MintToken(ctx context.Context, store TokenStore, req MintRequest) (*domain.Token, string, error) {
	if !req.Kind.Valid() {
		return nil, "", fmt.Errorf("%w: token kind must be %q or %q", domain.ErrInvalid, domain.TokenKindStandard, domain.TokenKindAdmin)
	}
	name, err := domain.NormalizeTokenName(req.Name)
	if err != nil {
		return nil, "", err
	}
	tags, err := domain.NormalizeTags(req.Tags)
	if err != nil {
		return nil, "", err
	}
	grants, err := ValidateGrants(ctx, store, req.Kind, req.Grants)
	if err != nil {
		return nil, "", err
	}
	if req.ExpiresIn < 0 || req.ExpiresIn > MaxTokenLifetime {
		return nil, "", fmt.Errorf("%w: expiry must be between 0 (never) and %d hours", domain.ErrInvalid, int(MaxTokenLifetime/time.Hour))
	}

	gen, err := NewToken()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	t := domain.Token{
		ID:         gen.ID,
		SecretHash: gen.SecretHash,
		Kind:       req.Kind,
		Name:       name,
		CreatedBy:  req.CreatedBy,
		Grants:     grants,
		Tags:       tags,
		CreatedAt:  now,
	}
	if req.ExpiresIn > 0 {
		exp := now.Add(req.ExpiresIn)
		t.ExpiresAt = &exp
	}
	if err := store.CreateToken(ctx, t); err != nil {
		return nil, "", err
	}
	t.SecretHash = ""
	return &t, gen.Plaintext, nil
}

// ValidateGrants normalizes grants for a token of the given kind. Admin
// tokens take no grants; standard tokens need at least one, and every named
// namespace must exist, so a typo'd namespace fails loudly at mint time
// instead of silently granting access to nothing.
func ValidateGrants(ctx context.Context, namespaces NamespaceChecker, kind domain.TokenKind, in []domain.GrantInput) ([]domain.Grant, error) {
	if kind == domain.TokenKindAdmin {
		if len(in) > 0 {
			return nil, fmt.Errorf("%w: admin tokens hold every permission by kind and take no grants", domain.ErrInvalid)
		}
		return nil, nil
	}
	grants, err := domain.NormalizeGrants(in)
	if err != nil {
		return nil, err
	}
	if len(grants) == 0 {
		return nil, fmt.Errorf("%w: a standard token needs at least one grant", domain.ErrInvalid)
	}
	checked := map[string]bool{}
	for _, g := range grants {
		if g.Namespace == domain.AllNamespaces || checked[g.Namespace] {
			continue
		}
		ok, err := namespaces.NamespaceExists(ctx, g.Namespace)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: namespace %q does not exist", domain.ErrInvalid, g.Namespace)
		}
		checked[g.Namespace] = true
	}
	return grants, nil
}
