package ui

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"coralline/app/domain"
)

type namespaceRepoStub struct {
	namespaces []domain.Namespace
}

func (s namespaceRepoStub) ListNamespaces(context.Context) ([]domain.Namespace, error) {
	return s.namespaces, nil
}

// moduleRepoStub serves a fixed set of published versions.
type moduleRepoStub struct {
	versions []domain.Version
}

func (s moduleRepoStub) ListPublishedVersions(_ context.Context, namespace string) ([]domain.Version, error) {
	var out []domain.Version
	for _, v := range s.versions {
		if namespace == "" || v.Namespace == namespace {
			out = append(out, v)
		}
	}
	return out, nil
}

var publishedAt = time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)

// published builds active versions of one module, oldest first, an hour
// apart.
func published(ns, name, system string, versions ...string) []domain.Version {
	out := make([]domain.Version, 0, len(versions))
	for i, v := range versions {
		out = append(out, domain.Version{
			Namespace: ns, Name: name, System: system, Version: v,
			Status: domain.VersionStatusActive, PublishedAt: publishedAt.Add(time.Duration(i) * time.Hour),
		})
	}
	return out
}

func modules(sets ...[]domain.Version) moduleRepoStub {
	var all []domain.Version
	for _, s := range sets {
		all = append(all, s...)
	}
	return moduleRepoStub{versions: all}
}

type publisherStub struct{}

func (publisherStub) PublisherName(context.Context, string, string, string, string) (string, error) {
	return "ci-publisher", nil
}

type tokenAdminStub struct {
	mu      sync.Mutex
	tokens  []domain.Token
	created *NewToken
	grants  []domain.GrantInput
	mints   int
}

func (s *tokenAdminStub) ListTokens(context.Context, domain.TokenFilter) ([]domain.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Token(nil), s.tokens...), nil
}

func (s *tokenAdminStub) GetToken(_ context.Context, id string) (*domain.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tokens {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *tokenAdminStub) CreateStandardToken(_ context.Context, in NewToken) (*domain.Token, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = &in
	s.mints++
	tok := domain.Token{ID: "newtoken", Name: in.Name, Kind: domain.TokenKindStandard, CreatedAt: time.Now()}
	for _, g := range in.Grants {
		tok.Grants = append(tok.Grants, domain.Grant{Namespace: g.Namespace, Permission: domain.Permission(g.Permission)})
	}
	s.tokens = append(s.tokens, tok)
	return &tok, "crl_pat_shown_once_example", nil
}

func (s *tokenAdminStub) ReplaceTags(context.Context, string, []domain.Tag) error { return nil }

func (s *tokenAdminStub) ReplaceGrants(_ context.Context, _ string, grants []domain.GrantInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grants = grants
	return nil
}

func (s *tokenAdminStub) RevokeToken(context.Context, string) error { return nil }

// revealStub is a single-session RevealStore.
type revealStub struct {
	mu      sync.Mutex
	claims  map[string]string // submission key → token id
	secrets map[string]string // token id → plaintext
}

func newRevealStub() *revealStub {
	return &revealStub{claims: map[string]string{}, secrets: map[string]string{}}
}

func (s *revealStub) Claim(_ *http.Request, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.claims[key]; ok {
		return id, false
	}
	s.claims[key] = ""
	return "", true
}

func (s *revealStub) Complete(_ *http.Request, key, tokenID, plaintext string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims[key] = tokenID
	s.secrets[tokenID] = plaintext
}

func (s *revealStub) Abandon(*http.Request, string) {}

func (s *revealStub) Take(_ *http.Request, tokenID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.secrets[tokenID]
	delete(s.secrets, tokenID)
	return v, ok
}

func openViewer(*http.Request) Viewer { return Viewer{ReadAll: true} }

func adminViewer(*http.Request) Viewer {
	return Viewer{Authenticated: true, Admin: true, Name: "bootstrap-admin", Role: "Admin", CSRF: "csrf", ReadAll: true}
}

func acmeOnlyViewer(*http.Request) Viewer {
	return Viewer{Authenticated: true, CSRF: "csrf", Readable: func(ns string) bool { return ns == "acme" }}
}

func newTestHandlers(t *testing.T, ns NamespaceRepo, mods ModuleRepo, viewer func(*http.Request) Viewer) *Handlers {
	t.Helper()
	h, err := NewHandlers(ns, mods)
	if err != nil {
		t.Fatal(err)
	}
	h.Viewer = viewer
	return h
}

func moduleRequest(target string) *http.Request {
	req := httptest.NewRequest("GET", target, nil)
	req.SetPathValue("ns", "acme")
	req.SetPathValue("name", "vpc")
	req.SetPathValue("system", "aws")
	return req
}

func TestNamespaceRendersPublishedModules(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, modules(published("acme", "vpc", "aws", "1.0.0")), openViewer)

	request := httptest.NewRequest("GET", "/n/acme", nil)
	request.SetPathValue("ns", "acme")
	recorder := httptest.NewRecorder()
	h.Namespace(recorder, request)
	body := recorder.Body.String()

	for _, want := range []string{`<h1>acme <span class="count">1</span></h1>`, `href="/n/acme/vpc/aws"`, `module-row__name">vpc<`, `module-row__version" title="1 version">1.0.0<`} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q:\n%s", want, body)
		}
	}
	// Inside a namespace, each row doesn't repeat the namespace.
	if strings.Contains(body, "in acme") {
		t.Error("namespace page repeated the namespace on each module")
	}
}

func TestIndexRendersNamespaceList(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{namespaces: []domain.Namespace{
		{Name: "acme", CreatedAt: time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)},
	}}, moduleRepoStub{}, openViewer)

	recorder := httptest.NewRecorder()
	h.Index(recorder, httptest.NewRequest("GET", "/namespaces", nil))
	body := recorder.Body.String()

	for _, want := range []string{`<h1>Namespaces <span class="count">1</span></h1>`, `href="/n/acme"`, ">acme</h2>", "Created Sep 12, 2026", "0 modules"} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "No modules are published") {
		t.Errorf("index rendered the namespace page template:\n%s", body)
	}
}

func TestHomeShowsCountsAndRecentModules(t *testing.T) {
	h := newTestHandlers(t,
		namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}, {Name: "obs"}}},
		modules(published("acme", "vpc", "aws", "1.0.0", "1.1.0"), published("obs", "dashboard", "newrelic", "0.1.0")),
		adminViewer)
	rr := httptest.NewRecorder()
	h.Home(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	for _, want := range []string{
		"Welcome back, bootstrap-admin",
		`Namespaces</span><strong class="stat-tile__value">2<`,
		`Modules</span><strong class="stat-tile__value">2<`,
		`Versions</span><strong class="stat-tile__value">3<`,
		`Providers</span><strong class="stat-tile__value">2<`,
		`href="/n/obs/dashboard/newrelic"`, "New Relic", "in obs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	// dashboard 0.1.0 was published at publishedAt and vpc 1.1.0 an hour
	// later, so vpc comes first.
	if v, d := strings.Index(body, `href="/n/acme/vpc/aws"`), strings.Index(body, `href="/n/obs/dashboard/newrelic"`); v > d {
		t.Error("recently published is not newest first")
	}
}

func TestEveryPageHasOneH1AndIsNotCached(t *testing.T) {
	h := newTestHandlers(t,
		namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}}},
		modules(published("acme", "vpc", "aws", "1.0.0")), adminViewer)
	h.TokenAdmin = &tokenAdminStub{tokens: []domain.Token{{ID: "tok1", Name: "ci", Kind: domain.TokenKindStandard}}}
	h.Reveals = newRevealStub()

	for path, handler := range map[string]http.HandlerFunc{
		"/":                 h.Home,
		"/namespaces":       h.Index,
		"/modules":          h.AllModules,
		"/n/acme":           h.Namespace,
		"/n/acme/vpc/aws":   h.Module,
		"/tokens":           h.TokenList,
		"/tokens/new":       h.NewTokenForm,
		"/tokens/tok1":      h.TokenDetail,
		"/tokens/tok1/made": h.TokenCreated,
		"/missing":          h.NotFound,
	} {
		req := moduleRequest(path)
		req.SetPathValue("id", "tok1")
		rr := httptest.NewRecorder()
		handler(rr, req)
		body := rr.Body.String()
		if n := strings.Count(body, "<h1"); n != 1 {
			t.Errorf("%s: %d <h1> elements, want 1", path, n)
		}
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", path, rr.Header().Get("Cache-Control"))
		}
		if strings.Contains(body, "<no value>") || strings.Contains(body, "&lt;no value&gt;") {
			t.Errorf("%s: rendered a missing template value", path)
		}
		// The sidebar's Publish guide is the one way to open the guide.
		if n := strings.Count(body, `data-component="guide-trigger"`); n != 1 {
			t.Errorf("%s: %d guide triggers, want 1 (the sidebar's)", path, n)
		}
	}
}

func TestEmptyPagesHaveNoExtraGuideButton(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, openViewer)
	for path, handler := range map[string]http.HandlerFunc{"/": h.Home, "/namespaces": h.Index, "/modules": h.AllModules} {
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest("GET", path, nil))
		if n := strings.Count(rr.Body.String(), `data-component="guide-trigger"`); n != 1 {
			t.Errorf("%s: %d guide triggers, want 1", path, n)
		}
	}
}

func TestNotFoundRendersStyledPageInShell(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, openViewer)
	rr := httptest.NewRecorder()
	h.NotFound(rr, httptest.NewRequest("GET", "/nope", nil))
	body := rr.Body.String()
	if rr.Code != http.StatusNotFound || !strings.Contains(body, "Page not found") || !strings.Contains(body, `class="sidebar"`) {
		t.Errorf("status %d:\n%s", rr.Code, body)
	}
}

func TestShellHasNoTopbarLabelOrHealthPill(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, adminViewer)
	rr := httptest.NewRecorder()
	h.Home(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	for _, gone := range []string{"health-status", "topbar-crumb", "Registry <span"} {
		if strings.Contains(body, gone) {
			t.Errorf("shell still renders %q", gone)
		}
	}
	for _, want := range []string{`class="icon-button sidebar__toggle"`, `data-component="theme-toggle"`, `action="/logout"`} {
		if !strings.Contains(body, want) {
			t.Errorf("sidebar missing %q", want)
		}
	}
}

func TestModuleVersionSelection(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, modules(published("acme", "vpc", "aws", "1.9.0", "1.10.0", "2.0.0-beta.1")), openViewer)
	for _, tc := range []struct {
		query, want, label string
		status             int
	}{
		{"", "1.10.0", `badge--success">Recommended`, 200},
		{"?version=1.9.0", "1.9.0", "", 200},
		{"?version=2.0.0-beta.1", "2.0.0-beta.1", `badge--warning">Pre-release`, 200},
		{"?version=missing", "", "", 404},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.Module(rr, moduleRequest("https://registry.example:8443/n/acme/vpc/aws"+tc.query))
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d", rr.Code, tc.status)
			}
			if tc.status != 200 {
				return
			}
			body := html.UnescapeString(rr.Body.String())
			for _, want := range []string{`source  = "registry.example:8443/acme/vpc/aws"`, `version = "` + tc.want + `"`, `data-target="usage-code"`, `<option value="` + tc.want + `" selected>`} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			// The selected version's label appears beside the selector.
			if tc.label != "" && !strings.Contains(body, tc.label) {
				t.Errorf("missing label %q", tc.label)
			}
		})
	}
}

func TestModulePageSaysEachThingOnce(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, modules(published("acme", "vpc", "aws", "1.0.0")), openViewer)
	rr := httptest.NewRecorder()
	h.Module(rr, moduleRequest("https://registry.example/n/acme/vpc/aws"))
	body := rr.Body.String()
	// The full address lives in the snippet and in the hidden copy source,
	// nowhere else: no eyebrow, no address subtitle, no details row.
	if n := strings.Count(body, "acme/vpc/aws"); n != 2 {
		t.Errorf("module address appears %d times, want 2 (snippet and copy source)", n)
	}
	for _, gone := range []string{`class="eyebrow"`, "<dt>Namespace</dt>", "<dt>Provider</dt>", "<dt>Source</dt>"} {
		if strings.Contains(body, gone) {
			t.Errorf("module page repeats %q", gone)
		}
	}
}

func TestModuleLabelsPrereleaseOnlyModulesAsLatest(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, modules(published("acme", "vpc", "aws", "1.0.0-rc.1", "1.0.0-rc.2")), openViewer)
	rr := httptest.NewRecorder()
	h.Module(rr, moduleRequest("/n/acme/vpc/aws"))
	body := rr.Body.String()
	if strings.Contains(body, "Recommended") || !strings.Contains(body, `badge--accent">Latest`) || !strings.Contains(body, `<option value="1.0.0-rc.2" selected>`) {
		t.Errorf("pre-release-only module labelling is wrong:\n%s", body)
	}
}

func TestHandlersFailClosedWithoutViewer(t *testing.T) {
	h, err := NewHandlers(namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}}},
		modules(published("acme", "vpc", "aws", "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}

	for path, handler := range map[string]http.HandlerFunc{"/": h.Home, "/namespaces": h.Index, "/modules": h.AllModules} {
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest("GET", path, nil))
		if strings.Contains(rr.Body.String(), `href="/n/acme`) {
			t.Errorf("%s listed a namespace without a viewer", path)
		}
	}

	req := httptest.NewRequest("GET", "/n/acme", nil)
	req.SetPathValue("ns", "acme")
	rr := httptest.NewRecorder()
	h.Namespace(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("namespace page status = %d, want 404", rr.Code)
	}
}

func TestListingsAreScopedToReadableNamespaces(t *testing.T) {
	h := newTestHandlers(t,
		namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}, {Name: "secret"}}},
		modules(published("acme", "vpc", "aws", "1.0.0"), published("secret", "vault", "aws", "1.0.0")),
		acmeOnlyViewer)

	for path, handler := range map[string]http.HandlerFunc{"/": h.Home, "/namespaces": h.Index, "/modules": h.AllModules} {
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest("GET", path, nil))
		body := rr.Body.String()
		if !strings.Contains(body, "acme") || strings.Contains(body, "secret") || strings.Contains(body, "vault") {
			t.Errorf("%s leaked or omitted namespaces:\n%s", path, body)
		}
	}

	req := httptest.NewRequest("GET", "/n/secret", nil)
	req.SetPathValue("ns", "secret")
	rr := httptest.NewRecorder()
	h.Namespace(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unreadable namespace status = %d, want 404", rr.Code)
	}

	req = httptest.NewRequest("GET", "/n/secret/vault/aws", nil)
	req.SetPathValue("ns", "secret")
	req.SetPathValue("name", "vault")
	req.SetPathValue("system", "aws")
	rr = httptest.NewRecorder()
	h.Module(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unreadable module status = %d, want 404", rr.Code)
	}
}

func TestAllModulesSortsByName(t *testing.T) {
	h := newTestHandlers(t,
		namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}, {Name: "beta"}}},
		modules(published("acme", "zebra", "aws", "1.0.0"), published("beta", "alpha", "aws", "1.0.0")),
		openViewer)
	rr := httptest.NewRecorder()
	h.AllModules(rr, httptest.NewRequest("GET", "/modules", nil))
	body := rr.Body.String()
	if a, z := strings.Index(body, `data-name="alpha beta"`), strings.Index(body, `data-name="zebra acme"`); a < 0 || z < 0 || a > z {
		t.Errorf("modules not in name order (alpha at %d, zebra at %d)", a, z)
	}
	for _, want := range []string{`<optgroup label="Cloud">`, `<option value="aws">AWS (2)</option>`, `data-updated="`} {
		if !strings.Contains(body, want) {
			t.Errorf("module catalog missing %q", want)
		}
	}
}

func TestTokensNavAndIdentityOnlyForAdmins(t *testing.T) {
	for name, tc := range map[string]struct {
		viewer    func(*http.Request) Viewer
		wantNav   bool
		wantName  bool
		wantLogin bool
	}{
		"admin":     {adminViewer, true, true, false},
		"standard":  {acmeOnlyViewer, false, false, false},
		"anonymous": {func(*http.Request) Viewer { return Viewer{ReadAll: true, LoginAvailable: true} }, false, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, tc.viewer)
			rr := httptest.NewRecorder()
			h.Home(rr, httptest.NewRequest("GET", "/", nil))
			body := rr.Body.String()
			if got := strings.Contains(body, `href="/tokens"`); got != tc.wantNav {
				t.Errorf("tokens nav shown = %v, want %v", got, tc.wantNav)
			}
			if got := strings.Contains(body, "bootstrap-admin"); got != tc.wantName {
				t.Errorf("token name shown = %v, want %v", got, tc.wantName)
			}
			if got := strings.Contains(body, `href="/login"`); got != tc.wantLogin {
				t.Errorf("sign-in link shown = %v, want %v", got, tc.wantLogin)
			}
		})
	}
}

func TestTokenPagesNotFoundForNonAdmins(t *testing.T) {
	stub := &tokenAdminStub{tokens: []domain.Token{{ID: "tok1", Name: "ci", Kind: domain.TokenKindStandard}}}
	for name, viewer := range map[string]func(*http.Request) Viewer{"standard": acmeOnlyViewer, "anonymous": openViewer} {
		h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, viewer)
		h.TokenAdmin = stub
		h.Reveals = newRevealStub()
		for path, handler := range map[string]http.HandlerFunc{
			"/tokens":              h.TokenList,
			"/tokens/new":          h.NewTokenForm,
			"/tokens/tok1":         h.TokenDetail,
			"/tokens/tok1/created": h.TokenCreated,
		} {
			req := httptest.NewRequest("GET", path, nil)
			req.SetPathValue("id", "tok1")
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != http.StatusNotFound {
				t.Errorf("%s viewer, %s: status = %d, want 404", name, path, rr.Code)
			}
		}
	}

	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, adminViewer)
	rr := httptest.NewRecorder()
	h.TokenList(rr, httptest.NewRequest("GET", "/tokens", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("admin with no TokenAdmin configured: status = %d, want 404", rr.Code)
	}
}

func TestModulePublisherOnlyForAdmins(t *testing.T) {
	for name, tc := range map[string]struct {
		viewer func(*http.Request) Viewer
		want   bool
	}{"admin": {adminViewer, true}, "standard": {openViewer, false}} {
		h := newTestHandlers(t, namespaceRepoStub{}, modules(published("acme", "vpc", "aws", "1.0.0")), tc.viewer)
		h.Publishers = publisherStub{}
		rr := httptest.NewRecorder()
		h.Module(rr, moduleRequest("/n/acme/vpc/aws"))
		if got := strings.Contains(rr.Body.String(), "ci-publisher"); got != tc.want {
			t.Errorf("%s: publisher shown = %v, want %v", name, got, tc.want)
		}
	}
}

func TestTokenListRendersAccessTagsAndStatus(t *testing.T) {
	now := time.Now()
	soon := now.Add(72 * time.Hour)
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, adminViewer)
	h.TokenAdmin = &tokenAdminStub{tokens: []domain.Token{
		{
			ID: "tok1", Name: "ci-publisher", Kind: domain.TokenKindStandard, CreatedAt: now,
			Grants: []domain.Grant{{Namespace: "acme", Permission: domain.PermModuleRead}, {Namespace: "acme", Permission: domain.PermModulePublish}},
			Tags:   map[string]string{"owner": "platform"},
		},
		{
			ID: "tok2", Name: "nr-dash", Kind: domain.TokenKindStandard, CreatedAt: now, ExpiresAt: &soon,
			Grants: []domain.Grant{{Namespace: domain.AllNamespaces, Permission: domain.PermModuleRead}},
		},
	}}
	rr := httptest.NewRecorder()
	h.TokenList(rr, httptest.NewRequest("GET", "/tokens", nil))
	body := rr.Body.String()
	for _, want := range []string{
		"ci-publisher", `href="/tokens/tok1"`, "publish · acme", "read · all namespaces", "owner=platform",
		`data-status="active"`, `data-status="expiring"`, "in 2 days", `data-facet="status"`,
		`data-search="ci-publisher owner=platform"`, "css/pages/tokens.css", `href="/tokens/tok1#revoke"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("token list missing %q", want)
		}
	}
}

func postForm(path string, form url.Values) *http.Request {
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func newTokenHandlers(t *testing.T) (*Handlers, *tokenAdminStub) {
	t.Helper()
	stub := &tokenAdminStub{}
	h := newTestHandlers(t, namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}}}, moduleRepoStub{}, adminViewer)
	h.TokenAdmin = stub
	h.Reveals = newRevealStub()
	return h, stub
}

func getCreated(h *Handlers, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "https://registry.example/tokens/"+id+"/created", nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	h.TokenCreated(rr, req)
	return rr
}

func TestCreateTokenRevealsThePlaintextOnce(t *testing.T) {
	h, stub := newTokenHandlers(t)
	rr := httptest.NewRecorder()
	h.CreateToken(rr, postForm("/tokens", url.Values{
		"_submission": {"key-1"},
		"name":        {"ci"}, "namespace": {"acme"}, "role": {"publisher"},
		"tags": {"owner=platform\n"}, "expires_in_hours": {"24"},
	}))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/tokens/newtoken/created" {
		t.Fatalf("status %d, location %q:\n%s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "crl_pat_") {
		t.Error("the POST response contained the plaintext")
	}
	if stub.created == nil || len(stub.created.Grants) != 2 || stub.created.ExpiresIn != 24*time.Hour || len(stub.created.Tags) != 1 {
		t.Fatalf("request passed to TokenAdmin = %+v", stub.created)
	}
	for _, g := range stub.created.Grants {
		if g.Namespace != "acme" {
			t.Errorf("grant namespace = %q", g.Namespace)
		}
	}

	first := getCreated(h, "newtoken")
	body := html.UnescapeString(first.Body.String())
	if first.Code != http.StatusOK || !strings.Contains(body, "crl_pat_shown_once_example") {
		t.Fatalf("first view: status %d, plaintext shown = false", first.Code)
	}
	for _, want := range []string{
		`export TF_TOKEN_registry_example="crl_pat_shown_once_example"`,
		`credentials "registry.example" {`,
		`export CORALLINE_TOKEN="crl_pat_shown_once_example"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reveal page missing snippet %q", want)
		}
	}
	if first.Header().Get("Cache-Control") != "no-store" {
		t.Error("reveal page is cacheable")
	}
	second := getCreated(h, "newtoken")
	if second.Code != http.StatusOK || strings.Contains(second.Body.String(), "crl_pat_") || !strings.Contains(second.Body.String(), "already shown") {
		t.Errorf("second view: status %d, still shows the secret or no explanation", second.Code)
	}
}

func TestCreateTokenExpiryPresets(t *testing.T) {
	for name, tc := range map[string]struct {
		form url.Values
		want time.Duration
		err  string
	}{
		"preset":            {url.Values{"expiry": {"168"}, "expires_in_hours": {"5"}}, 168 * time.Hour, ""},
		"never":             {url.Values{"expiry": {"0"}}, 0, ""},
		"custom":            {url.Values{"expiry": {"custom"}, "expires_in_hours": {"36"}}, 36 * time.Hour, ""},
		"custom but empty":  {url.Values{"expiry": {"custom"}}, 0, "Enter how many hours the token lasts"},
		"no preset (older)": {url.Values{"expires_in_hours": {"12"}}, 12 * time.Hour, ""},
	} {
		t.Run(name, func(t *testing.T) {
			h, stub := newTokenHandlers(t)
			form := url.Values{"_submission": {"k"}, "name": {"ci"}, "namespace": {"acme"}, "role": {"reader"}}
			for k, v := range tc.form {
				form[k] = v
			}
			rr := httptest.NewRecorder()
			h.CreateToken(rr, postForm("/tokens", form))
			if tc.err != "" {
				if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.err) {
					t.Fatalf("status %d, want 400 mentioning %q", rr.Code, tc.err)
				}
				return
			}
			if stub.created == nil || stub.created.ExpiresIn != tc.want {
				t.Fatalf("expiry = %+v, want %v", stub.created, tc.want)
			}
		})
	}
}

func TestExpiryOptionsRespectMax(t *testing.T) {
	opts := expiryOptions(30 * 24)
	var labels []string
	for _, o := range opts {
		labels = append(labels, o.Label)
	}
	if got := strings.Join(labels, ","); got != "7 days,30 days,Never,Custom" {
		t.Errorf("options = %s", got)
	}
}

func TestCreateTokenResubmissionDoesNotMintAgain(t *testing.T) {
	h, stub := newTokenHandlers(t)
	form := url.Values{"_submission": {"key-1"}, "name": {"ci"}, "namespace": {"acme"}, "role": {"reader"}}
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		h.CreateToken(rr, postForm("/tokens", form))
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/tokens/newtoken/created" {
			t.Fatalf("submission %d: status %d, location %q", i+1, rr.Code, rr.Header().Get("Location"))
		}
	}
	if stub.mints != 1 {
		t.Errorf("minted %d tokens for one submission key, want 1", stub.mints)
	}
}

func TestCreateTokenRejectsInvalidForms(t *testing.T) {
	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"missing submission key": {url.Values{"name": {"ci"}, "namespace": {"acme"}, "role": {"reader"}}, "This form expired"},
		"custom without permissions": {url.Values{"_submission": {"k"}, "name": {"ci"}, "namespace": {"acme"}, "role": {"custom"}},
			"Choose at least one custom permission"},
		"permissions without custom": {url.Values{"_submission": {"k"}, "name": {"ci"}, "namespace": {"acme"}, "role": {"reader"}, "permission": {"module:publish"}},
			"Custom permissions only apply when Access is set to Custom"},
	} {
		t.Run(name, func(t *testing.T) {
			h, stub := newTokenHandlers(t)
			rr := httptest.NewRecorder()
			h.CreateToken(rr, postForm("/tokens", tc.form))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("status = %d:\n%s", rr.Code, rr.Body.String())
			}
			if stub.created != nil {
				t.Error("invalid form reached TokenAdmin")
			}
			if !strings.Contains(rr.Body.String(), `name="_submission" value="`) {
				t.Error("re-rendered form has no fresh submission key")
			}
		})
	}
}

func TestUpdateTokenAccessLevels(t *testing.T) {
	custom := domain.Grant{Namespace: "legacy", Permission: domain.PermModuleAll}
	tok := domain.Token{ID: "tok1", Name: "ci", Kind: domain.TokenKindStandard, Grants: []domain.Grant{custom}}

	post := func(form url.Values) (*httptest.ResponseRecorder, *tokenAdminStub) {
		h, stub := newTokenHandlers(t)
		stub.tokens = []domain.Token{tok}
		req := postForm("/tokens/tok1/grants", form)
		req.SetPathValue("id", "tok1")
		rr := httptest.NewRecorder()
		h.UpdateTokenGrants(rr, req)
		return rr, stub
	}

	rr, stub := post(url.Values{"access:acme": {"publish"}, "access:*": {"read"}, "access:other": {"none"}, "access:legacy": {"custom"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status %d:\n%s", rr.Code, rr.Body.String())
	}
	got := map[string]bool{}
	for _, g := range stub.grants {
		got[g.Namespace+"|"+g.Permission] = true
	}
	for _, want := range []string{"acme|module:read", "acme|module:publish", "*|module:read", "legacy|module:*"} {
		if !got[want] {
			t.Errorf("grants %v missing %s", stub.grants, want)
		}
	}
	if len(stub.grants) != 4 {
		t.Errorf("grants = %v, want exactly 4", stub.grants)
	}

	rr, stub = post(url.Values{"access:acme": {"owner"}})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Choose None, Read, or Publish for acme") || stub.grants != nil {
		t.Errorf("unknown level: status %d, grants %v", rr.Code, stub.grants)
	}
}

func TestTokenDetailShowsAccessLevels(t *testing.T) {
	h, stub := newTokenHandlers(t)
	stub.tokens = []domain.Token{{ID: "tok1", Name: "ci", Kind: domain.TokenKindStandard, Grants: []domain.Grant{
		{Namespace: "acme", Permission: domain.PermModuleRead},
		{Namespace: "legacy", Permission: domain.PermModuleAll},
	}}}
	req := httptest.NewRequest("GET", "/tokens/tok1", nil)
	req.SetPathValue("id", "tok1")
	rr := httptest.NewRecorder()
	h.TokenDetail(rr, req)
	body := rr.Body.String()
	for _, want := range []string{
		`name="access:acme" value="read" checked`,
		`name="access:*" value="none" checked`,
		`name="access:legacy" value="custom" checked`,
		`data-component="tag-editor"`, `id="revoke"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
}

func TestRenderLoginIsStandalone(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, openViewer)
	rr := httptest.NewRecorder()
	h.RenderLogin(rr, httptest.NewRequest("GET", "/login", nil), LoginPage{Next: "/modules", Error: "That token isn't valid."}, http.StatusUnauthorized)
	body := rr.Body.String()
	if rr.Code != http.StatusUnauthorized || rr.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("status %d, cache-control %q", rr.Code, rr.Header().Get("Cache-Control"))
	}
	for _, want := range []string{`name="token"`, `value="/modules"`, "css/pages/login.css", "alert--error"} {
		if !strings.Contains(body, want) {
			t.Errorf("login page missing %q", want)
		}
	}
	if strings.Contains(body, `class="sidebar"`) || strings.Contains(body, "app-shell") {
		t.Error("login page rendered the app shell")
	}
}

func TestCompareVersions(t *testing.T) {
	ordered := []string{"1.9.0", "1.10.0-alpha", "1.10.0-alpha.2", "1.10.0-alpha.10", "1.10.0-beta", "1.10.0", "2.0.0"}
	for i := 1; i < len(ordered); i++ {
		if compareVersions(ordered[i-1], ordered[i]) >= 0 || compareVersions(ordered[i], ordered[i-1]) <= 0 {
			t.Errorf("incorrect order: %s, %s", ordered[i-1], ordered[i])
		}
	}
	if compareVersions("1.0.0+first", "1.0.0+second") != 0 {
		t.Error("build metadata affected precedence")
	}
}

func TestLatestStable(t *testing.T) {
	for _, tc := range []struct {
		sorted     []string
		want       string
		wantStable bool
	}{
		{[]string{"2.0.0-beta.1", "1.10.0", "1.9.0"}, "1.10.0", true},
		{[]string{"1.0.0-rc.2", "1.0.0-rc.1"}, "1.0.0-rc.2", false},
		{[]string{"1.0.0+build"}, "1.0.0+build", true},
		{nil, "", false},
	} {
		got, stable := latestStable(tc.sorted)
		if got != tc.want || stable != tc.wantStable {
			t.Errorf("latestStable(%v) = %q, %v; want %q, %v", tc.sorted, got, stable, tc.want, tc.wantStable)
		}
	}
}

func TestSummarize(t *testing.T) {
	got := summarize(append(published("acme", "vpc", "aws", "1.9.0", "1.10.0", "2.0.0-beta.1"), published("acme", "db", "aws", "0.1.0")...))
	if len(got) != 2 || got[0].Name != "db" || got[1].Name != "vpc" {
		t.Fatalf("summaries = %+v", got)
	}
	vpc := got[1]
	if vpc.Latest != "1.10.0" || !vpc.LatestIsStable || vpc.VersionCount != 3 || !vpc.UpdatedAt.Equal(publishedAt.Add(2*time.Hour)) {
		t.Errorf("vpc summary = %+v", vpc)
	}
}

func TestProviders(t *testing.T) {
	if p := lookupProvider("newrelic"); p.Name != "New Relic" || p.Category != catObservability {
		t.Errorf("newrelic = %+v", p)
	}
	unknown := lookupProvider("acmecorp")
	if unknown.Name != "Acmecorp" || unknown.Category != catOther || unknown.Tone != lookupProvider("acmecorp").Tone {
		t.Errorf("acmecorp = %+v", unknown)
	}
	if n, err := strconv.Atoi(unknown.Tone); err != nil || n < 0 || n >= toneCount {
		t.Errorf("unknown provider tone %q is not a hue", unknown.Tone)
	}
	groups := groupProviders([]moduleSummary{
		{Module: domain.Module{System: "acmecorp"}}, {Module: domain.Module{System: "aws"}},
		{Module: domain.Module{System: "google"}}, {Module: domain.Module{System: "google"}},
	})
	if len(groups) != 2 || groups[0].Category != catCloud || groups[0].Options[0].System != "google" || groups[1].Category != catOther {
		t.Errorf("groups = %+v", groups)
	}
}

func TestTerraformEnvVar(t *testing.T) {
	for host, want := range map[string]string{
		"registry.example.com":  "TF_TOKEN_registry_example_com",
		"my-registry.io":        "TF_TOKEN_my__registry_io",
		"registry.example:8443": "",
	} {
		if got := terraformEnvVar(host); got != want {
			t.Errorf("terraformEnvVar(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestTemplateFuncs(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ got, want string }{
		{plural(0, "module", "modules"), "0 modules"},
		{plural(1, "module", "modules"), "1 module"},
		{plural(2, "module", "modules"), "2 modules"},
		{initial("acme"), "A"},
		{initial("émile"), "É"},
		{initial(""), "?"},
		{capitalize("émile"), "Émile"},
		{formatDate(time.Date(2026, 9, 2, 23, 0, 0, 0, time.FixedZone("x", -5*3600))), "Sep 3, 2026"},
		{ago(now.Add(-30*time.Second), now), "just now"},
		{ago(now.Add(-3*time.Hour), now), "3h ago"},
		{ago(now.Add(-40*24*time.Hour), now), "1mo ago"},
		{ago(time.Time{}, now), "—"},
		{until(now.Add(72*time.Hour), now), "in 3 days"},
		{until(now.Add(-time.Hour), now), ""},
		{greeting(Viewer{Authenticated: true}), "Welcome back"},
		{greeting(Viewer{}), "Welcome to Coralline"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
	if hashTone("acme") != hashTone("acme") || hashTone("acme") < 0 || hashTone("acme") >= toneCount {
		t.Error("tone is not a stable hue index")
	}
	if _, err := dict("odd"); err == nil {
		t.Error("dict accepted an odd number of arguments")
	}
	if _, err := icon("no-such-icon"); err == nil {
		t.Error("icon accepted an unknown name")
	}
}

func TestStandardViewerSeesOwnNameAndRole(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, func(*http.Request) Viewer {
		return Viewer{Authenticated: true, Name: "alice-laptop", Role: "Reader", CSRF: "csrf", Readable: func(string) bool { return true }}
	})
	rr := httptest.NewRecorder()
	h.Home(rr, httptest.NewRequest("GET", "/", nil))
	body := rr.Body.String()
	for _, want := range []string{`identity__name">alice-laptop<`, `identity__role">Reader<`, "Welcome back, alice-laptop"} {
		if !strings.Contains(body, want) {
			t.Errorf("home missing %q", want)
		}
	}
	if strings.Contains(body, `href="/tokens"`) {
		t.Error("standard viewer sees the Tokens section")
	}
}
