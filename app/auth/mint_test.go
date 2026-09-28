package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"coralline/app/domain"
)

type fakeTokenStore struct {
	namespaces map[string]bool
	tokens     map[string]domain.Token
	order      []string
}

func newFakeTokenStore(namespaces ...string) *fakeTokenStore {
	f := &fakeTokenStore{namespaces: map[string]bool{}, tokens: map[string]domain.Token{}}
	for _, ns := range namespaces {
		f.namespaces[ns] = true
	}
	return f
}

func (f *fakeTokenStore) CreateToken(_ context.Context, t domain.Token) error {
	f.tokens[t.ID] = t
	f.order = append(f.order, t.ID)
	return nil
}

func (f *fakeTokenStore) NamespaceExists(_ context.Context, name string) (bool, error) {
	return f.namespaces[name], nil
}

func (f *fakeTokenStore) CountActiveAdminTokens(context.Context) (int, error) {
	n := 0
	for _, t := range f.tokens {
		if t.IsAdmin() && t.Active(time.Now()) {
			n++
		}
	}
	return n, nil
}

func (f *fakeTokenStore) GetToken(_ context.Context, id string) (*domain.Token, error) {
	t, ok := f.tokens[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &t, nil
}

type recordedUse struct{ ids []string }

func (r *recordedUse) Record(id string, _ time.Time) { r.ids = append(r.ids, id) }

func TestMintTokenStandard(t *testing.T) {
	store := newFakeTokenStore("acme")
	tok, plaintext, err := MintToken(context.Background(), store, MintRequest{
		Kind:      domain.TokenKindStandard,
		Name:      "  ci publisher ",
		CreatedBy: "admin-id",
		Grants: []domain.GrantInput{
			{Namespace: "acme", Permission: "module:publish"},
			{Namespace: "acme", Permission: "module:publish"},
		},
		Tags:      []domain.Tag{{Key: "Owner", Value: "alice"}},
		ExpiresIn: 2 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "ci publisher" || tok.CreatedBy != "admin-id" || len(tok.Grants) != 1 || tok.Tags["owner"] != "alice" {
		t.Fatalf("minted token = %+v", tok)
	}
	if tok.SecretHash != "" {
		t.Error("returned token still carries the secret hash")
	}
	if tok.ExpiresAt == nil || time.Until(*tok.ExpiresAt) < time.Hour {
		t.Errorf("expiry = %v", tok.ExpiresAt)
	}

	stored := store.tokens[tok.ID]
	id, secret, ok := ParseToken(plaintext)
	if !ok || id != tok.ID {
		t.Fatalf("plaintext does not parse to the token id")
	}
	if !VerifySecret(secret, stored.SecretHash) {
		t.Fatal("stored hash does not verify the plaintext secret")
	}
}

func TestMintTokenRejectsInvalidRequests(t *testing.T) {
	valid := []domain.GrantInput{{Namespace: "acme", Permission: "module:read"}}
	for name, req := range map[string]MintRequest{
		"unknown permission":  {Kind: domain.TokenKindStandard, Name: "x", Grants: []domain.GrantInput{{Namespace: "acme", Permission: "module:pubish"}}},
		"admin-only string":   {Kind: domain.TokenKindStandard, Name: "x", Grants: []domain.GrantInput{{Namespace: "acme", Permission: "token:create"}}},
		"missing namespace":   {Kind: domain.TokenKindStandard, Name: "x", Grants: []domain.GrantInput{{Namespace: "typo", Permission: "module:read"}}},
		"standard, no grants": {Kind: domain.TokenKindStandard, Name: "x"},
		"admin with grants":   {Kind: domain.TokenKindAdmin, Name: "x", Grants: valid},
		"empty kind":          {Name: "x", Grants: valid},
		"bad kind":            {Kind: "superuser", Name: "x", Grants: valid},
		"bad name":            {Kind: domain.TokenKindStandard, Name: "", Grants: valid},
		"bad tag":             {Kind: domain.TokenKindStandard, Name: "x", Grants: valid, Tags: []domain.Tag{{Key: "bad key", Value: "v"}}},
		"negative expiry":     {Kind: domain.TokenKindStandard, Name: "x", Grants: valid, ExpiresIn: -time.Hour},
		"absurd expiry":       {Kind: domain.TokenKindStandard, Name: "x", Grants: valid, ExpiresIn: MaxTokenLifetime + time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			store := newFakeTokenStore("acme")
			if _, _, err := MintToken(context.Background(), store, req); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if len(store.tokens) != 0 {
				t.Fatal("an invalid request still created a token")
			}
		})
	}
}

func TestMintTokenAllowsWildcardNamespaceWithoutLookup(t *testing.T) {
	store := newFakeTokenStore() // no namespaces at all
	_, _, err := MintToken(context.Background(), store, MintRequest{
		Kind: domain.TokenKindStandard, Name: "global-reader",
		Grants: []domain.GrantInput{{Namespace: "*", Permission: "module:read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnsureAdminTokenMintsOnlyWhenNoneActive(t *testing.T) {
	ctx := context.Background()
	store := newFakeTokenStore()

	plaintext, err := EnsureAdminToken(ctx, store)
	if err != nil || plaintext == "" {
		t.Fatalf("first boot: plaintext %q, err %v", plaintext, err)
	}
	first := store.tokens[store.order[0]]
	if !first.IsAdmin() || first.Name != BootstrapAdminName {
		t.Fatalf("bootstrap token = %+v", first)
	}

	if again, err := EnsureAdminToken(ctx, store); err != nil || again != "" {
		t.Fatalf("second boot minted again: %q, %v", again, err)
	}

	revoked := time.Now().Add(-time.Minute)
	first.RevokedAt = &revoked
	store.tokens[first.ID] = first
	if recovered, err := EnsureAdminToken(ctx, store); err != nil || recovered == "" {
		t.Fatalf("lockout recovery did not mint: %q, %v", recovered, err)
	}
}

func TestAuthenticateTokenChecksStateAndRecordsUse(t *testing.T) {
	ctx := context.Background()
	store := newFakeTokenStore("acme")
	used := &recordedUse{}
	v := &Verifier{Store: store, Semaphore: NewArgonSemaphore(1), LastUsed: used}

	tok, plaintext, err := MintToken(ctx, store, MintRequest{
		Kind: domain.TokenKindStandard, Name: "reader",
		Grants: []domain.GrantInput{{Namespace: "acme", Permission: "module:read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/login", nil)

	got, err := v.AuthenticateToken(req, "  "+plaintext+"\n")
	if err != nil || got.ID != tok.ID {
		t.Fatalf("valid token: %v, %v", got, err)
	}
	if len(used.ids) != 1 || used.ids[0] != tok.ID {
		t.Errorf("last-used records = %v", used.ids)
	}

	if _, err := v.AuthenticateToken(req, plaintext[:len(plaintext)-1]+"X"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("corrupted token: err = %v", err)
	}

	stored := store.tokens[tok.ID]
	revoked := time.Now().Add(-time.Second)
	stored.RevokedAt = &revoked
	store.tokens[tok.ID] = stored
	if _, err := v.AuthenticateToken(req, plaintext); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("revoked token: err = %v", err)
	}
	if len(used.ids) != 1 {
		t.Errorf("failed authentications recorded use: %v", used.ids)
	}

	busy := &Verifier{Store: store, Semaphore: NewArgonSemaphore(1)}
	busy.Semaphore.TryAcquire()
	if _, err := busy.AuthenticateToken(req, plaintext); !errors.Is(err, domain.ErrRateLimited) {
		t.Errorf("saturated semaphore: err = %v, want ErrRateLimited", err)
	}
}
