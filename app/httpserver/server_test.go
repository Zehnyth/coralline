package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"coralline/app/auth"
	"coralline/app/domain"
	"coralline/app/modules"
	"coralline/app/sqlitestore"
	"coralline/app/storage"
)

const piiEmail = "alice@corp.example"

type testRegistry struct {
	t          *testing.T
	db         *sqlitestore.DB
	verifier   *auth.Verifier
	server     *httptest.Server
	adminToken string
	adminID    string
	userToken  string
	userID     string
}

// newTestRegistry boots the real public handler over TLS (session cookies
// are Secure) with two namespaces, an admin token, and a standard token
// that can read only "acme" and carries a PII tag.
func newTestRegistry(t *testing.T, requireLogin bool) *testRegistry {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()

	db, err := sqlitestore.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := storage.NewFSStore(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, ns := range []string{"acme", "secret"} {
		if err := db.CreateNamespace(ctx, ns); err != nil {
			t.Fatal(err)
		}
	}
	adminToken, err := auth.EnsureAdminToken(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	adminID, _, _ := auth.ParseToken(adminToken)

	user, userToken, err := auth.MintToken(ctx, db, auth.MintRequest{
		Kind: domain.TokenKindStandard, Name: "alice-reader", CreatedBy: adminID,
		Grants: []domain.GrantInput{{Namespace: "acme", Permission: "module:read"}},
		Tags:   []domain.Tag{{Key: "email", Value: piiEmail}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"acme", "secret"} {
		if err := db.ReserveVersion(ctx, ns, "vpc", "aws", "1.0.0", user.ID); err != nil {
			t.Fatal(err)
		}
		if err := db.ActivateVersion(ctx, ns, "vpc", "aws", "1.0.0", "sha", 1); err != nil {
			t.Fatal(err)
		}
	}

	verifier := &auth.Verifier{
		Store:     db,
		Semaphore: auth.NewArgonSemaphore(4),
		Limiter:   auth.NewIPRateLimiter(1000, time.Minute, 100),
	}
	handler, err := New(&Deps{
		DB:           db,
		Blobs:        blobs,
		Publisher:    &modules.Publisher{Repo: db, Blobs: blobs, Log: logger},
		Verifier:     verifier,
		Attributor:   sqlitestore.NewDownloadAttributor(db, logger),
		Logger:       logger,
		RequireLogin: requireLogin,
		MaxUpload:    1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)

	return &testRegistry{t: t, db: db, verifier: verifier, server: server,
		adminToken: adminToken, adminID: adminID, userToken: userToken, userID: user.ID}
}

// browser is a cookie-keeping client that doesn't follow redirects.
func (tr *testRegistry) browser() *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		tr.t.Fatal(err)
	}
	return &http.Client{
		Transport:     tr.server.Client().Transport,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (tr *testRegistry) do(c *http.Client, req *http.Request) (*http.Response, string) {
	tr.t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		tr.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		tr.t.Fatal(err)
	}
	return resp, string(b)
}

func (tr *testRegistry) get(c *http.Client, path string) (*http.Response, string) {
	tr.t.Helper()
	req, err := http.NewRequest("GET", tr.server.URL+path, nil)
	if err != nil {
		tr.t.Fatal(err)
	}
	return tr.do(c, req)
}

func (tr *testRegistry) post(c *http.Client, path string, form url.Values, origin string) (*http.Response, string) {
	tr.t.Helper()
	req, err := http.NewRequest("POST", tr.server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		tr.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return tr.do(c, req)
}

func (tr *testRegistry) login(c *http.Client, token string) {
	tr.t.Helper()
	resp, body := tr.post(c, "/login", url.Values{"token": {token}, "next": {"/modules"}}, tr.server.URL)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/modules" {
		tr.t.Fatalf("login: status %d, location %q:\n%s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
}

// csrfFrom pulls the session's CSRF value out of any rendered app page.
func csrfFrom(t *testing.T, body string) string {
	t.Helper()
	return fieldFrom(t, body, "_csrf")
}

// fieldFrom pulls a hidden form field's value out of a rendered page.
func fieldFrom(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no %s field in page:\n%s", name, body)
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

// An escaped slash survives routing as part of one path value, so the
// publish handler must reject it before it can shape a blob key.
func TestPublishRejectsEscapedSlashInCoordinates(t *testing.T) {
	tr := newTestRegistry(t, true)
	req, err := http.NewRequest("PUT", tr.server.URL+"/v1/modules/acme/a%2Fb/aws/1.0.0/publish", strings.NewReader("archive"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tr.adminToken)
	resp, body := tr.do(tr.server.Client(), req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400:\n%s", resp.StatusCode, body)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                              "/",
		"/modules":                      "/modules",
		"/n/acme?version=1.0.0":         "/n/acme?version=1.0.0",
		"//evil.example":                "/",
		"/\\evil.example":               "/",
		"/\t/evil.example":              "/",
		"https://evil.example/":         "/",
		"evil.example":                  "/",
		"javascript:alert(1)":           "/",
		"/login":                        "/",
		"/logout?x=1":                   "/",
		"/%0d%0aSet-Cookie:x=1":         "/%0d%0aSet-Cookie:x=1", // stays escaped, same-origin path
		"/" + strings.Repeat("a", 3000): "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionStoreExpiry(t *testing.T) {
	now := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	s := newSessionStore()
	s.now = func() time.Time { return now }

	sess, err := s.create("tok")
	if err != nil {
		t.Fatal(err)
	}
	if sess.id == "" || sess.csrf == "" || sess.id == sess.csrf {
		t.Fatalf("session = %+v", sess)
	}

	now = now.Add(sessionIdleTimeout - time.Minute)
	if _, ok := s.get(sess.id); !ok {
		t.Fatal("active session expired early")
	}
	now = now.Add(sessionIdleTimeout + time.Minute)
	if _, ok := s.get(sess.id); ok {
		t.Fatal("idle session did not expire")
	}

	sess, _ = s.create("tok")
	for i := 0; i < 30; i++ { // keep it busy past the absolute lifetime
		now = now.Add(sessionAbsoluteTimeout / 25)
		s.get(sess.id)
	}
	if _, ok := s.get(sess.id); ok {
		t.Fatal("session outlived its absolute lifetime")
	}
}

func TestRequireLoginRedirectsAnonymousVisitors(t *testing.T) {
	tr := newTestRegistry(t, true)
	c := tr.browser()

	resp, _ := tr.get(c, "/modules")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?next=%2Fmodules" {
		t.Fatalf("anonymous /modules: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = tr.get(c, "/tokens")
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("anonymous /tokens: %d, want redirect to sign-in like every other page", resp.StatusCode)
	}
	for _, path := range []string{"/login", "/static/css/base.css", "/readyz"} {
		if resp, _ := tr.get(c, path); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200 without signing in", path, resp.StatusCode)
		}
	}
	if resp, _ := tr.get(c, "/v1/modules/acme/vpc/aws/versions"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous registry read: status %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("security headers missing")
	}
}

func TestLoginRejectsCrossOriginAndInvalidTokens(t *testing.T) {
	tr := newTestRegistry(t, true)
	c := tr.browser()

	resp, _ := tr.post(c, "/login", url.Values{"token": {tr.userToken}}, "https://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin login: status %d, want 403", resp.StatusCode)
	}
	resp, _ = tr.post(c, "/login", url.Values{"token": {tr.userToken}}, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("login without Origin: status %d, want 403", resp.StatusCode)
	}
	resp, body := tr.post(c, "/login", url.Values{"token": {"crl_pat_not_a_real_token"}}, tr.server.URL)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Check that you pasted") {
		t.Errorf("invalid token: status %d", resp.StatusCode)
	}
	if resp, _ := tr.get(c, "/modules"); resp.StatusCode != http.StatusSeeOther {
		t.Error("a failed login started a session")
	}

	resp, _ = tr.post(c, "/login", url.Values{"token": {tr.userToken}, "next": {"//evil.example"}}, tr.server.URL)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Errorf("open redirect: location %q, want /", resp.Header.Get("Location"))
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == sessionCookieName && (!cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode) {
			t.Errorf("session cookie flags: %+v", cookie)
		}
	}
}

func TestStandardTokenCannotDiscoverTokensOrOtherNamespaces(t *testing.T) {
	tr := newTestRegistry(t, true)
	c := tr.browser()
	tr.login(c, tr.userToken)

	resp, body := tr.get(c, "/modules")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/modules: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `href="/n/acme/vpc/aws"`) || strings.Contains(body, `href="/n/secret`) {
		t.Errorf("module listing not scoped to readable namespaces:\n%s", body)
	}
	// The session sees its own token name and role, never anyone else's.
	if !strings.Contains(body, "alice-reader") || !strings.Contains(body, ">Reader<") {
		t.Error("sidebar does not show the signed-in token's own name and role")
	}
	for _, leak := range []string{`href="/tokens"`, "bootstrap-admin", piiEmail} {
		if strings.Contains(body, leak) {
			t.Errorf("standard session page contains %q", leak)
		}
	}
	csrf := csrfFrom(t, body)

	for _, path := range []string{"/n/secret", "/n/secret/vpc/aws", "/tokens", "/tokens/new", "/tokens/" + tr.adminID, "/tokens/" + tr.userID} {
		if resp, _ := tr.get(c, path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/tokens", "/tokens/" + tr.userID + "/grants", "/tokens/" + tr.adminID + "/revoke"} {
		form := url.Values{"_csrf": {csrf}, "name": {"escalate"}, "grant": {"*|module:*"}}
		if resp, _ := tr.post(c, path, form, tr.server.URL); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s: status %d, want 404", path, resp.StatusCode)
		}
	}

	_, body = tr.get(c, "/n/acme/vpc/aws")
	if strings.Contains(body, "Published by") {
		t.Error("module page showed publisher attribution to a standard session")
	}

	req, _ := http.NewRequest("GET", tr.server.URL+"/v1/modules/acme/vpc/aws/versions", nil)
	req.Header.Set("Authorization", "Bearer "+tr.userToken)
	resp, body = tr.do(c, req)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, piiEmail) {
		t.Errorf("registry read: status %d, body %s", resp.StatusCode, body)
	}
}

func TestAdminManagesTokensThroughUI(t *testing.T) {
	tr := newTestRegistry(t, true)
	admin := tr.browser()
	tr.login(admin, tr.adminToken)

	resp, body := tr.get(admin, "/tokens")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "alice-reader") || !strings.Contains(body, piiEmail) {
		t.Fatalf("admin token list: status %d", resp.StatusCode)
	}
	_, body = tr.get(admin, "/n/acme/vpc/aws")
	if !strings.Contains(body, "Published by") || !strings.Contains(body, "alice-reader") {
		t.Error("admin module page is missing publisher attribution")
	}

	_, body = tr.get(admin, "/tokens/new")
	csrf := csrfFrom(t, body)
	create := url.Values{"name": {"ui-publisher"}, "namespace": {"acme"}, "role": {"publisher"}, "tags": {"owner=bob"},
		"_submission": {fieldFrom(t, body, "_submission")}}

	if resp, _ := tr.post(admin, "/tokens", create, tr.server.URL); resp.StatusCode != http.StatusForbidden {
		t.Errorf("create without CSRF: status %d, want 403", resp.StatusCode)
	}
	withCSRF := url.Values{"_csrf": {csrf}}
	for k, v := range create {
		withCSRF[k] = v
	}
	if resp, _ := tr.post(admin, "/tokens", withCSRF, "https://evil.example"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin create: status %d, want 403", resp.StatusCode)
	}

	// Creating redirects to a one-time reveal; the secret is never in the
	// POST response or the URL.
	resp, body = tr.post(admin, "/tokens", withCSRF, tr.server.URL)
	reveal := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(reveal, "/created") || strings.Contains(body, "crl_pat_") {
		t.Fatalf("create: status %d, location %q", resp.StatusCode, reveal)
	}
	resp, body = tr.get(admin, reveal)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "crl_pat_") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal: status %d, cache-control %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if resp, body = tr.get(admin, reveal); resp.StatusCode != http.StatusOK || strings.Contains(body, "crl_pat_") {
		t.Errorf("second reveal: status %d, secret shown again = %v", resp.StatusCode, strings.Contains(body, "crl_pat_"))
	}
	// Resubmitting the same form (Back + submit, a double click) doesn't mint again.
	if resp, _ = tr.post(admin, "/tokens", withCSRF, tr.server.URL); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != reveal {
		t.Errorf("resubmission: status %d, location %q, want %q", resp.StatusCode, resp.Header.Get("Location"), reveal)
	}
	toks, err := tr.db.ListTokens(context.Background(), domain.TokenFilter{TagKey: "owner", TagValue: "bob"})
	if err != nil || len(toks) != 1 {
		t.Fatalf("created token not found: %v, %v", toks, err)
	}
	created := toks[0]
	if created.IsAdmin() || created.CreatedBy != tr.adminID || !created.CanPublish("acme") || created.CanRead("secret") {
		t.Errorf("created token = %+v", created)
	}

	escalate := url.Values{"_csrf": {csrf}, "name": {"sneaky"}, "namespace": {"acme"}, "role": {"custom"}, "permission": {"token:create"},
		"_submission": {"escalate-attempt"}}
	resp, body = tr.post(admin, "/tokens", escalate, tr.server.URL)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Unknown permission") {
		t.Errorf("ungrantable permission: status %d", resp.StatusCode)
	}

	// Admin tokens are read-only in the UI, whatever the page shows.
	for _, path := range []string{"/tokens/" + tr.adminID + "/revoke", "/tokens/" + tr.adminID + "/tags"} {
		resp, _ = tr.post(admin, path, url.Values{"_csrf": {csrf}, "tags": {"owner=mallory"}}, tr.server.URL)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s on an admin token from the UI: status %d, want 400", path, resp.StatusCode)
		}
	}
	if n, err := tr.db.CountActiveAdminTokens(context.Background()); err != nil || n != 1 {
		t.Errorf("active admin tokens = %d, %v; want 1", n, err)
	}
}

func TestRevokingATokenEndsItsSession(t *testing.T) {
	tr := newTestRegistry(t, true)
	user := tr.browser()
	tr.login(user, tr.userToken)
	if resp, _ := tr.get(user, "/modules"); resp.StatusCode != http.StatusOK {
		t.Fatalf("signed-in user: status %d", resp.StatusCode)
	}

	admin := tr.browser()
	tr.login(admin, tr.adminToken)
	_, body := tr.get(admin, "/tokens/"+tr.userID)
	resp, _ := tr.post(admin, "/tokens/"+tr.userID+"/revoke", url.Values{"_csrf": {csrfFrom(t, body)}}, tr.server.URL)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: status %d", resp.StatusCode)
	}

	if resp, _ := tr.get(user, "/modules"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("revoked token's session still works: status %d", resp.StatusCode)
	}
}

func TestAnonymousModeStillHidesTokens(t *testing.T) {
	tr := newTestRegistry(t, false)
	c := tr.browser()

	resp, body := tr.get(c, "/modules")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `href="/n/secret/vpc/aws"`) {
		t.Fatalf("anonymous listing: status %d", resp.StatusCode)
	}
	for _, leak := range []string{`href="/tokens"`, piiEmail, "alice-reader"} {
		if strings.Contains(body, leak) {
			t.Errorf("anonymous page contains %q", leak)
		}
	}
	if !strings.Contains(body, `href="/login"`) {
		t.Error("no sign-in link for admins in anonymous mode")
	}
	if resp, _ := tr.get(c, "/tokens"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("anonymous /tokens: status %d, want 404", resp.StatusCode)
	}
	if resp, _ := tr.get(c, "/v1/modules/secret/vpc/aws/versions"); resp.StatusCode != http.StatusOK {
		t.Errorf("anonymous registry read with require_login off: status %d", resp.StatusCode)
	}
}

func TestAdminAPIAuthentication(t *testing.T) {
	tr := newTestRegistry(t, true)
	api := httptest.NewServer(NewAdminServer(tr.db, tr.verifier, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(api.Close)

	call := func(method, path, token string) int {
		req, _ := http.NewRequest(method, api.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := call("GET", "/tokens", ""); got != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", got)
	}
	if got := call("GET", "/tokens", tr.userToken); got != http.StatusForbidden {
		t.Errorf("standard token: %d, want 403", got)
	}
	if got := call("GET", "/tokens", tr.adminToken); got != http.StatusOK {
		t.Errorf("admin token: %d, want 200", got)
	}
	if got := call("DELETE", "/tokens/"+tr.adminID, tr.adminToken); got != http.StatusConflict {
		t.Errorf("revoking the last admin: %d, want 409", got)
	}

	// A separate verifier, saturated before its server starts, proves admin
	// authentication now runs behind the argon2id semaphore.
	busy := &auth.Verifier{Store: tr.db, Semaphore: auth.NewArgonSemaphore(1)}
	busy.Semaphore.TryAcquire()
	busyAPI := httptest.NewServer(NewAdminServer(tr.db, busy, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(busyAPI.Close)
	req, _ := http.NewRequest("GET", busyAPI.URL+"/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+tr.adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("saturated argon2id semaphore: %d, want 429", resp.StatusCode)
	}
}

// A token's secret is only ever revealed to the browser session that
// minted it, even another session of the same admin token.
func TestTokenRevealIsBoundToTheMintingSession(t *testing.T) {
	tr := newTestRegistry(t, true)
	minter, other := tr.browser(), tr.browser()
	tr.login(minter, tr.adminToken)
	tr.login(other, tr.adminToken)

	_, body := tr.get(minter, "/tokens/new")
	form := url.Values{"_csrf": {csrfFrom(t, body)}, "_submission": {fieldFrom(t, body, "_submission")},
		"name": {"bound"}, "namespace": {"acme"}, "role": {"reader"}}
	resp, _ := tr.post(minter, "/tokens", form, tr.server.URL)
	reveal := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: status %d", resp.StatusCode)
	}
	if _, body := tr.get(other, reveal); strings.Contains(body, "crl_pat_") {
		t.Error("another session saw the new token's secret")
	}
	if _, body := tr.get(minter, reveal); !strings.Contains(body, "crl_pat_") {
		t.Error("the minting session could not see its secret")
	}
}

func TestRevealStoreForgetsWithTheSession(t *testing.T) {
	sessions := newSessionStore()
	reveals := newRevealStore(sessions)
	sess, err := sessions.create("tok")
	if err != nil {
		t.Fatal(err)
	}
	withSession := func(s session) *http.Request {
		req := httptest.NewRequest("GET", "/", nil)
		return req.WithContext(context.WithValue(req.Context(), viewerContextKey{}, requestViewer{session: s}))
	}
	req := withSession(sess)

	if _, fresh := reveals.Claim(req, "k"); !fresh {
		t.Fatal("first claim not fresh")
	}
	reveals.Complete(req, "k", "t1", "secret")
	if id, fresh := reveals.Claim(req, "k"); fresh || id != "t1" {
		t.Fatalf("duplicate claim = %q, %v", id, fresh)
	}

	sessions.delete(sess.id)
	if _, ok := reveals.Take(req, "t1"); ok {
		t.Error("secret outlived its session")
	}

	// Expired secrets are never shown.
	sess, err = sessions.create("tok")
	if err != nil {
		t.Fatal(err)
	}
	req = withSession(sess)
	now := time.Now()
	reveals.now = func() time.Time { return now }
	reveals.Claim(req, "k2")
	reveals.Complete(req, "k2", "t2", "secret")
	now = now.Add(revealTTL + time.Second)
	if _, ok := reveals.Take(req, "t2"); ok {
		t.Error("expired secret was revealed")
	}
}

func TestPagesAreNotCachedButAssetsAre(t *testing.T) {
	tr := newTestRegistry(t, true)
	c := tr.browser()
	tr.login(c, tr.userToken)

	resp, body := tr.get(c, "/modules")
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("/modules Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
	}
	const marker = `rel="stylesheet" href="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("no stylesheet link")
	}
	href := body[i+len(marker):]
	href = href[:strings.Index(href, `"`)]
	resp, _ = tr.get(c, href)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("%s: status %d, Cache-Control %q", href, resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
}

func TestUnknownPathsAnswerNotFound(t *testing.T) {
	tr := newTestRegistry(t, true)
	c := tr.browser()
	tr.login(c, tr.userToken)

	resp, body := tr.get(c, "/no/such/page")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "Page not found") {
		t.Errorf("unknown page: status %d", resp.StatusCode)
	}
	resp, body = tr.get(c, "/v1/no/such/endpoint")
	if resp.StatusCode != http.StatusNotFound || strings.Contains(body, "<html") {
		t.Errorf("unknown registry path: status %d, HTML body = %v", resp.StatusCode, strings.Contains(body, "<html"))
	}
}

// Terraform and OpenTofu fetch the X-Terraform-Get location through
// go-getter, which never sends host credentials, so with sign-in required
// the archive has to be reachable through the signed URL download returns.
func TestDownloadedArchiveURLWorksWithoutCredentials(t *testing.T) {
	tr := newTestRegistry(t, true)
	c := tr.server.Client()
	const archive = "not-really-a-tarball"

	req, _ := http.NewRequest("PUT", tr.server.URL+"/v1/modules/acme/vpc/aws/2.0.0/publish", strings.NewReader(archive))
	req.Header.Set("Authorization", "Bearer "+tr.adminToken)
	if resp, body := tr.do(c, req); resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish: status %d: %s", resp.StatusCode, body)
	}

	req, _ = http.NewRequest("GET", tr.server.URL+"/v1/modules/acme/vpc/aws/2.0.0/download", nil)
	req.Header.Set("Authorization", "Bearer "+tr.userToken)
	resp, _ := tr.do(c, req)
	getURL := resp.Header.Get("X-Terraform-Get")
	if resp.StatusCode != http.StatusNoContent || !strings.Contains(getURL, "sig=") {
		t.Fatalf("download: status %d, X-Terraform-Get %q", resp.StatusCode, getURL)
	}

	// Followed exactly as go-getter does: no Authorization header.
	if resp, body := tr.get(c, getURL); resp.StatusCode != http.StatusOK || body != archive {
		t.Errorf("signed archive URL without credentials: status %d, body %q", resp.StatusCode, body)
	}

	// Without the signature, or with a tampered one, credentials are needed.
	for _, path := range []string{
		"/v1/modules/acme/vpc/aws/2.0.0/archive?archive=tar.gz",
		strings.Replace(getURL, "/2.0.0/", "/1.0.0/", 1),
		strings.Replace(getURL, "sig=", "sig=ff", 1),
	} {
		if resp, _ := tr.get(c, path); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without credentials: status %d, want 401", path, resp.StatusCode)
		}
	}
}
