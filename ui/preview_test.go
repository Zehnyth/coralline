package ui

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"coralline/app/domain"
)

// TestBrowsePreview serves only synthetic fixtures for visual review.
// Run with CORALLINE_UI_PREVIEW=1 and go test ./ui -run TestBrowsePreview -timeout 0.
// CORALLINE_UI_PREVIEW_ROLE picks the viewer: admin (default), standard, or anonymous.
// CORALLINE_UI_PREVIEW_ADDR sets the listen address (default 127.0.0.1:8787;
// use 0.0.0.0:8787 inside a container and publish the port).
func TestBrowsePreview(t *testing.T) {
	if os.Getenv("CORALLINE_UI_PREVIEW") != "1" {
		t.Skip("opt-in browser preview")
	}
	role := os.Getenv("CORALLINE_UI_PREVIEW_ROLE")
	if role == "" {
		role = "admin"
	}
	repo := previewRepo{}
	h, err := NewHandlers(repo, repo, repo)
	if err != nil {
		t.Fatal(err)
	}
	h.Viewer = previewViewer(role)
	h.Publishers = repo
	h.TokenAdmin = newPreviewTokens()
	h.Reveals = newPreviewReveals()
	// Serve CSS and JS from disk, uncached, so visual iterations only need a
	// browser reload. Templates are parsed at startup; restart for those.
	if h.Assets, err = NewAssets(os.DirFS("static"), true); err != nil {
		t.Fatal(err)
	}
	addr := os.Getenv("CORALLINE_UI_PREVIEW_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8787"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.Home)
	mux.HandleFunc("GET /namespaces", h.Index)
	mux.HandleFunc("GET /modules", h.AllModules)
	mux.HandleFunc("GET /n/{ns}", h.Namespace)
	mux.HandleFunc("GET /n/{ns}/{name}/{system}", h.Module)
	mux.HandleFunc("GET /tokens", h.TokenList)
	mux.HandleFunc("GET /tokens/new", h.NewTokenForm)
	mux.HandleFunc("POST /tokens", h.CreateToken)
	mux.HandleFunc("GET /tokens/{id}", h.TokenDetail)
	mux.HandleFunc("GET /tokens/{id}/created", h.TokenCreated)
	mux.HandleFunc("POST /tokens/{id}/grants", h.UpdateTokenGrants)
	mux.HandleFunc("POST /tokens/{id}/tags", h.UpdateTokenTags)
	mux.HandleFunc("POST /tokens/{id}/revoke", h.RevokeToken)
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		h.RenderLogin(w, r, LoginPage{Next: "/"}, http.StatusOK)
	})
	mux.Handle("GET /static/", h.Assets)
	mux.HandleFunc("GET /", h.NotFound)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	t.Logf("Synthetic UI preview as %s viewer on %s (sign-in page at /login)", role, addr)
	t.Fatal(http.ListenAndServe(addr, mux))
}

func previewViewer(role string) func(*http.Request) Viewer {
	switch role {
	case "anonymous":
		return func(*http.Request) Viewer { return Viewer{ReadAll: true, LoginAvailable: true} }
	case "standard":
		return func(*http.Request) Viewer {
			return Viewer{Authenticated: true, CSRF: "preview", Readable: func(ns string) bool { return ns == "acme" }}
		}
	default:
		return func(*http.Request) Viewer {
			return Viewer{Authenticated: true, Admin: true, Name: "bootstrap-admin", CSRF: "preview", ReadAll: true}
		}
	}
}

type previewRepo struct{}

func (previewRepo) ListNamespaces(context.Context) ([]domain.Namespace, error) {
	result := []domain.Namespace{}
	for _, n := range []string{"acme", "platform", "sandbox"} {
		result = append(result, domain.Namespace{Name: n, CreatedAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)})
	}
	return result, nil
}

var previewModules = map[string][]domain.Module{
	"acme":     {{Namespace: "acme", Name: "subnet", System: "aws"}, {Namespace: "acme", Name: "vpc", System: "aws"}},
	"platform": {{Namespace: "platform", Name: "cluster", System: "aws"}, {Namespace: "platform", Name: "project", System: "google"}},
	"sandbox":  {{Namespace: "sandbox", Name: "resource-group", System: "azurerm"}},
}

func (previewRepo) ListModules(context.Context) ([]domain.Module, error) {
	var out []domain.Module
	for _, ns := range []string{"acme", "platform", "sandbox"} {
		out = append(out, previewModules[ns]...)
	}
	return out, nil
}
func (previewRepo) ListModulesInNamespace(_ context.Context, ns string) ([]domain.Module, error) {
	return append([]domain.Module(nil), previewModules[ns]...), nil
}
func (previewRepo) ListActiveVersions(_ context.Context, ns, name, system string) ([]string, error) {
	if name == "vpc" {
		return []string{"1.0.0", "1.1.0"}, nil
	}
	if name == "cluster" {
		return []string{"2.0.0", "2.1.0"}, nil
	}
	return []string{"1.0.0"}, nil
}
func (previewRepo) PublisherName(context.Context, string, string, string, string) (string, error) {
	return "ci-publisher", nil
}

// previewTokens is an in-memory TokenAdmin so every Tokens page and form can
// be exercised visually. It applies the domain validation rules but mints
// nothing real.
type previewTokens struct {
	mu     sync.Mutex
	tokens []domain.Token
}

func newPreviewTokens() *previewTokens {
	now := time.Now().UTC()
	lastUsed := now.Add(-2 * time.Hour)
	expires := now.Add(30 * 24 * time.Hour)
	revoked := now.Add(-24 * time.Hour)
	return &previewTokens{tokens: []domain.Token{
		{ID: "adm0preview0000000000000000", Kind: domain.TokenKindAdmin, Name: "bootstrap-admin", CreatedAt: now.Add(-30 * 24 * time.Hour), LastUsedAt: &now},
		{ID: "ci00preview0000000000000000", Kind: domain.TokenKindStandard, Name: "ci-publisher", CreatedBy: "adm0preview0000000000000000", CreatedByName: "bootstrap-admin",
			CreatedAt: now.Add(-14 * 24 * time.Hour), LastUsedAt: &lastUsed,
			Grants: []domain.Grant{{Namespace: "acme", Permission: domain.PermModulePublish}, {Namespace: "acme", Permission: domain.PermModuleRead}},
			Tags:   map[string]string{"owner": "platform-team", "pipeline": "github-actions"}},
		{ID: "ali0preview0000000000000000", Kind: domain.TokenKindStandard, Name: "alice laptop", CreatedBy: "adm0preview0000000000000000", CreatedByName: "bootstrap-admin",
			CreatedAt: now.Add(-3 * 24 * time.Hour), ExpiresAt: &expires,
			Grants: []domain.Grant{{Namespace: domain.AllNamespaces, Permission: domain.PermModuleRead}},
			Tags:   map[string]string{"owner": "alice", "email": "alice@example.com", "team": "networking"}},
		{ID: "old0preview0000000000000000", Kind: domain.TokenKindStandard, Name: "old-ci", CreatedAt: now.Add(-90 * 24 * time.Hour), RevokedAt: &revoked,
			Grants: []domain.Grant{{Namespace: "sandbox", Permission: domain.PermModuleAll}}},
	}}
}

func (p *previewTokens) ListTokens(_ context.Context, filter domain.TokenFilter) ([]domain.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []domain.Token
	for _, t := range p.tokens {
		if filter.TagKey == "" || (t.Tags != nil && t.Tags[filter.TagKey] == filter.TagValue) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (p *previewTokens) GetToken(_ context.Context, id string) (*domain.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.tokens {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (p *previewTokens) CreateStandardToken(_ context.Context, in NewToken) (*domain.Token, string, error) {
	name, err := domain.NormalizeTokenName(in.Name)
	if err != nil {
		return nil, "", err
	}
	grants, err := domain.NormalizeGrants(in.Grants)
	if err != nil {
		return nil, "", err
	}
	tags, err := domain.NormalizeTags(in.Tags)
	if err != nil {
		return nil, "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	tok := domain.Token{
		ID: "new" + now.Format("150405") + "preview0000000", Kind: domain.TokenKindStandard, Name: name,
		CreatedBy: "adm0preview0000000000000000", CreatedByName: "bootstrap-admin", CreatedAt: now,
		Grants: grants, Tags: tags,
	}
	if in.ExpiresIn > 0 {
		exp := now.Add(in.ExpiresIn)
		tok.ExpiresAt = &exp
	}
	p.tokens = append(p.tokens, tok)
	return &tok, "crl_pat_preview_only_this_is_not_a_real_token", nil
}

func (p *previewTokens) ReplaceTags(_ context.Context, id string, tags []domain.Tag) error {
	normalized, err := domain.NormalizeTags(tags)
	if err != nil {
		return err
	}
	return p.update(id, func(t *domain.Token) error {
		t.Tags = normalized
		return nil
	})
}

func (p *previewTokens) ReplaceGrants(_ context.Context, id string, grants []domain.GrantInput) error {
	normalized, err := domain.NormalizeGrants(grants)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return domain.ErrInvalid
	}
	return p.update(id, func(t *domain.Token) error {
		t.Grants = normalized
		return nil
	})
}

func (p *previewTokens) RevokeToken(_ context.Context, id string) error {
	return p.update(id, func(t *domain.Token) error {
		now := time.Now().UTC()
		t.RevokedAt = &now
		return nil
	})
}

func (p *previewTokens) update(id string, change func(*domain.Token) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.tokens {
		if p.tokens[i].ID == id {
			return change(&p.tokens[i])
		}
	}
	return domain.ErrNotFound
}

// previewReveals is a single-viewer RevealStore for the preview.
type previewReveals struct {
	mu      sync.Mutex
	claims  map[string]string
	secrets map[string]string
}

func newPreviewReveals() *previewReveals {
	return &previewReveals{claims: map[string]string{}, secrets: map[string]string{}}
}

func (p *previewReveals) Claim(_ *http.Request, key string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.claims[key]; ok {
		return id, false
	}
	p.claims[key] = ""
	return "", true
}

func (p *previewReveals) Complete(_ *http.Request, key, tokenID, plaintext string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.claims[key] = tokenID
	p.secrets[tokenID] = plaintext
}

func (p *previewReveals) Abandon(*http.Request, string) {}

func (p *previewReveals) Take(_ *http.Request, tokenID string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.secrets[tokenID]
	delete(p.secrets, tokenID)
	return v, ok
}
