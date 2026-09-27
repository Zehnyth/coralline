package ui

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
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

type moduleRepoStub struct {
	modules []domain.Module
}

func (s moduleRepoStub) ListModules(context.Context) ([]domain.Module, error) {
	return append([]domain.Module(nil), s.modules...), nil
}

func (s moduleRepoStub) ListModulesInNamespace(context.Context, string) ([]domain.Module, error) {
	return append([]domain.Module(nil), s.modules...), nil
}

// modulesByNamespace returns each namespace's own modules.
type modulesByNamespace map[string][]domain.Module

func (m modulesByNamespace) ListModules(context.Context) ([]domain.Module, error) {
	names := make([]string, 0, len(m))
	for ns := range m {
		names = append(names, ns)
	}
	sort.Strings(names)
	var out []domain.Module
	for _, ns := range names {
		out = append(out, m[ns]...)
	}
	return out, nil
}

func (m modulesByNamespace) ListModulesInNamespace(_ context.Context, ns string) ([]domain.Module, error) {
	return append([]domain.Module(nil), m[ns]...), nil
}

type versionRepoStub struct{ versions []string }

func (s versionRepoStub) ListActiveVersions(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), s.versions...), nil
}

type publisherStub struct{}

func (publisherStub) PublisherName(context.Context, string, string, string, string) (string, error) {
	return "ci-publisher", nil
}

type tokenAdminStub struct {
	mu      sync.Mutex
	tokens  []domain.Token
	created *NewToken
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
	s.tokens = append(s.tokens, tok)
	return &tok, "crl_pat_shown_once_example", nil
}

func (s *tokenAdminStub) ReplaceTags(context.Context, string, []domain.Tag) error { return nil }

func (s *tokenAdminStub) ReplaceGrants(context.Context, string, []domain.GrantInput) error {
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
	return Viewer{Authenticated: true, Admin: true, Name: "bootstrap-admin", CSRF: "csrf", ReadAll: true}
}

func acmeOnlyViewer(*http.Request) Viewer {
	return Viewer{Authenticated: true, CSRF: "csrf", Readable: func(ns string) bool { return ns == "acme" }}
}

func newTestHandlers(t *testing.T, ns NamespaceRepo, mods ModuleRepo, vers VersionRepo, viewer func(*http.Request) Viewer) *Handlers {
	t.Helper()
	h, err := NewHandlers(ns, mods, vers)
	if err != nil {
		t.Fatal(err)
	}
	h.Viewer = viewer
	return h
}

func TestNamespaceRendersPublishedModules(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{modules: []domain.Module{
		{Namespace: "acme", Name: "vpc", System: "aws"},
	}}, versionRepoStub{}, openViewer)

	request := httptest.NewRequest("GET", "/n/acme", nil)
	request.SetPathValue("ns", "acme")
	recorder := httptest.NewRecorder()
	h.Namespace(recorder, request)
	body := recorder.Body.String()

	for _, want := range []string{"<h1>acme</h1>", `href="/n/acme/vpc/aws"`, "vpc / aws", "1 module"} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q:\n%s", want, body)
		}
	}
}

func TestIndexRendersNamespaceList(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{namespaces: []domain.Namespace{
		{Name: "acme", CreatedAt: time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)},
	}}, moduleRepoStub{}, versionRepoStub{}, openViewer)

	recorder := httptest.NewRecorder()
	h.Index(recorder, httptest.NewRequest("GET", "/namespaces", nil))
	body := recorder.Body.String()

	for _, want := range []string{"<h1>Namespaces</h1>", `href="/n/acme"`, ">acme</h2>", "Created Sep 12, 2026", "0 modules", "css/pages/namespaces.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("response does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "No modules published") {
		t.Errorf("index rendered the namespace page template:\n%s", body)
	}
}

func TestEveryPageHasOneH1AndIsNotCached(t *testing.T) {
	h := newTestHandlers(t,
		namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}}},
		moduleRepoStub{modules: []domain.Module{{Namespace: "acme", Name: "vpc", System: "aws"}}},
		versionRepoStub{versions: []string{"1.0.0"}}, adminViewer)
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
		req := httptest.NewRequest("GET", path, nil)
		req.SetPathValue("ns", "acme")
		req.SetPathValue("name", "vpc")
		req.SetPathValue("system", "aws")
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
	}
}

func TestNotFoundRendersStyledPageInShell(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{}, openViewer)
	rr := httptest.NewRecorder()
	h.NotFound(rr, httptest.NewRequest("GET", "/nope", nil))
	body := rr.Body.String()
	if rr.Code != http.StatusNotFound || !strings.Contains(body, "Page not found") || !strings.Contains(body, `class="sidebar"`) {
		t.Errorf("status %d:\n%s", rr.Code, body)
	}
}

func TestModuleVersionSelection(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{versions: []string{"1.9.0", "1.10.0", "2.0.0-beta.1"}}, openViewer)
	for _, tc := range []struct {
		query, want string
		status      int
	}{
		{"", "1.10.0", 200},
		{"?version=1.9.0", "1.9.0", 200},
		{"?version=2.0.0-beta.1", "2.0.0-beta.1", 200},
		{"?version=missing", "", 404},
	} {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "https://registry.example:8443/n/acme/vpc/aws"+tc.query, nil)
			req.SetPathValue("ns", "acme")
			req.SetPathValue("name", "vpc")
			req.SetPathValue("system", "aws")
			rr := httptest.NewRecorder()
			h.Module(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d", rr.Code, tc.status)
			}
			if tc.status == 200 {
				body := html.UnescapeString(rr.Body.String())
				for _, want := range []string{`source  = "registry.example:8443/acme/vpc/aws"`, `version = "` + tc.want + `"`, "Copy configuration", "Recommended"} {
					if !strings.Contains(body, want) {
						t.Errorf("missing %q", want)
					}
				}
			}
		})
	}
}

func TestModuleLabelsPrereleaseOnlyModulesAsLatest(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{versions: []string{"1.0.0-rc.1", "1.0.0-rc.2"}}, openViewer)
	req := httptest.NewRequest("GET", "/n/acme/vpc/aws", nil)
	req.SetPathValue("ns", "acme")
	req.SetPathValue("name", "vpc")
	req.SetPathValue("system", "aws")
	rr := httptest.NewRecorder()
	h.Module(rr, req)
	body := rr.Body.String()
	if strings.Contains(body, "Recommended") || !strings.Contains(body, "Latest") || !strings.Contains(body, "1.0.0-rc.2 · Latest") {
		t.Errorf("pre-release-only module labelling is wrong:\n%s", body)
	}
}

func TestHandlersFailClosedWithoutViewer(t *testing.T) {
	h, err := NewHandlers(namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}}},
		moduleRepoStub{modules: []domain.Module{{Namespace: "acme", Name: "vpc", System: "aws"}}},
		versionRepoStub{versions: []string{"1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}

	for path, handler := range map[string]http.HandlerFunc{"/namespaces": h.Index, "/modules": h.AllModules} {
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
		modulesByNamespace{
			"acme":   {{Namespace: "acme", Name: "vpc", System: "aws"}},
			"secret": {{Namespace: "secret", Name: "vault", System: "aws"}},
		},
		versionRepoStub{versions: []string{"1.0.0"}},
		acmeOnlyViewer)

	for path, handler := range map[string]http.HandlerFunc{"/namespaces": h.Index, "/modules": h.AllModules} {
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
		modulesByNamespace{
			"acme": {{Namespace: "acme", Name: "zebra", System: "aws"}},
			"beta": {{Namespace: "beta", Name: "alpha", System: "aws"}},
		},
		versionRepoStub{}, openViewer)
	rr := httptest.NewRecorder()
	h.AllModules(rr, httptest.NewRequest("GET", "/modules", nil))
	body := rr.Body.String()
	if a, z := strings.Index(body, `data-name="alpha beta"`), strings.Index(body, `data-name="zebra acme"`); a < 0 || z < 0 || a > z {
		t.Errorf("modules not in name order (alpha at %d, zebra at %d)", a, z)
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
			h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{}, tc.viewer)
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
		h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{}, viewer)
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

	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{}, adminViewer)
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
		h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{versions: []string{"1.0.0"}}, tc.viewer)
		h.Publishers = publisherStub{}
		req := httptest.NewRequest("GET", "/n/acme/vpc/aws", nil)
		req.SetPathValue("ns", "acme")
		req.SetPathValue("name", "vpc")
		req.SetPathValue("system", "aws")
		rr := httptest.NewRecorder()
		h.Module(rr, req)
		if got := strings.Contains(rr.Body.String(), "ci-publisher"); got != tc.want {
			t.Errorf("%s: publisher shown = %v, want %v", name, got, tc.want)
		}
	}
}

func TestTokenListRendersGrantsAndTags(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{}, adminViewer)
	h.TokenAdmin = &tokenAdminStub{tokens: []domain.Token{{
		ID: "tok1", Name: "ci-publisher", Kind: domain.TokenKindStandard,
		Grants: []domain.Grant{{Namespace: "acme", Permission: domain.PermModulePublish}},
		Tags:   map[string]string{"owner": "platform"},
	}}}
	rr := httptest.NewRecorder()
	h.TokenList(rr, httptest.NewRequest("GET", "/tokens", nil))
	body := rr.Body.String()
	for _, want := range []string{"ci-publisher", `href="/tokens/tok1"`, "module:publish", "<b>owner</b>=platform", "css/pages/tokens.css"} {
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
	h := newTestHandlers(t, namespaceRepoStub{namespaces: []domain.Namespace{{Name: "acme"}}}, moduleRepoStub{}, versionRepoStub{}, adminViewer)
	h.TokenAdmin = stub
	h.Reveals = newRevealStub()
	return h, stub
}

func getCreated(h *Handlers, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/tokens/"+id+"/created", nil)
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
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "crl_pat_shown_once_example") {
		t.Fatalf("first view: status %d, plaintext shown = false", first.Code)
	}
	if first.Header().Get("Cache-Control") != "no-store" {
		t.Error("reveal page is cacheable")
	}
	second := getCreated(h, "newtoken")
	if second.Code != http.StatusOK || strings.Contains(second.Body.String(), "crl_pat_") || !strings.Contains(second.Body.String(), "already shown") {
		t.Errorf("second view: status %d, still shows the secret or no explanation", second.Code)
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

func TestRenderLoginIsStandalone(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{}, openViewer)
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

func TestTemplateFuncs(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{plural(0, "module", "modules"), "0 modules"},
		{plural(1, "module", "modules"), "1 module"},
		{plural(2, "module", "modules"), "2 modules"},
		{initial("acme"), "A"},
		{initial("émile"), "É"},
		{initial(""), "?"},
		{capitalize("émile"), "Émile"},
		{formatDate(time.Date(2026, 9, 2, 23, 0, 0, 0, time.FixedZone("x", -5*3600))), "Sep 3, 2026"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
	if tone("acme") != tone("acme") || tone("acme") < 0 || tone("acme") > 2 {
		t.Error("tone is not a stable value in 0..2")
	}
	if _, err := dict("odd"); err == nil {
		t.Error("dict accepted an odd number of arguments")
	}
	if _, err := icon("no-such-icon"); err == nil {
		t.Error("icon accepted an unknown name")
	}
}
