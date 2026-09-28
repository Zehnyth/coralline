package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"coralline/app/auth"
	"coralline/app/domain"
	"coralline/app/logging"
	"coralline/ui"
)

const (
	sessionCookieName      = "coralline_session"
	csrfFieldName          = "_csrf"
	csrfHeaderName         = "X-CSRF-Token"
	sessionIdleTimeout     = time.Hour
	sessionAbsoluteTimeout = 12 * time.Hour
	maxSessions            = 10_000
	maxLoginFormBytes      = 8 << 10
	maxUIFormBytes         = 64 << 10
)

// session maps a random browser cookie to a token id. Sessions live in
// memory only: they end when the process restarts, which is correct and
// means there is no session table to manage or leak.
type session struct {
	id        string
	tokenID   string
	csrf      string
	createdAt time.Time
	lastSeen  time.Time
}

type sessionStore struct {
	mu   sync.Mutex
	byID map[string]*session
	now  func() time.Time
	// onDrop, when set, runs (with mu held) for every session removed —
	// signed out, expired, or evicted — so per-session state kept elsewhere
	// (pending token reveals) ends with it.
	onDrop func(id string)
}

func newSessionStore() *sessionStore {
	return &sessionStore{byID: map[string]*session{}, now: time.Now}
}

func (s *sessionStore) create(tokenID string) (session, error) {
	id, err := randomValue()
	if err != nil {
		return session{}, err
	}
	csrf, err := randomValue()
	if err != nil {
		return session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.byID) >= maxSessions {
		s.evictLocked(now)
	}
	sess := &session{id: id, tokenID: tokenID, csrf: csrf, createdAt: now, lastSeen: now}
	s.byID[id] = sess
	return *sess, nil
}

func (s *sessionStore) get(id string) (session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return session{}, false
	}
	now := s.now()
	if s.expired(sess, now) {
		s.dropLocked(id)
		return session{}, false
	}
	sess.lastSeen = now
	return *sess, true
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropLocked(id)
}

func (s *sessionStore) dropLocked(id string) {
	if _, ok := s.byID[id]; !ok {
		return
	}
	delete(s.byID, id)
	if s.onDrop != nil {
		s.onDrop(id)
	}
}

func (s *sessionStore) expired(sess *session, now time.Time) bool {
	return now.Sub(sess.lastSeen) > sessionIdleTimeout || now.Sub(sess.createdAt) > sessionAbsoluteTimeout
}

// evictLocked sweeps expired sessions and, if the store is still full,
// drops the least recently seen one, so memory stays bounded.
func (s *sessionStore) evictLocked(now time.Time) {
	var oldestID string
	var oldest time.Time
	for id, sess := range s.byID {
		if s.expired(sess, now) {
			s.dropLocked(id)
			continue
		}
		if oldestID == "" || sess.lastSeen.Before(oldest) {
			oldestID, oldest = id, sess.lastSeen
		}
	}
	if len(s.byID) >= maxSessions && oldestID != "" {
		s.dropLocked(oldestID)
	}
}

func randomValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type viewerContextKey struct{}

// requestViewer is the resolved identity behind a browser request; token is
// nil for anonymous visitors.
type requestViewer struct {
	token   *domain.Token
	session session
}

func viewerFrom(ctx context.Context) requestViewer {
	rv, _ := ctx.Value(viewerContextKey{}).(requestViewer)
	return rv
}

// uiGate owns browser authentication for the public listener: sign-in,
// sessions, the require_login redirect, the admin-only gate, and CSRF.
// The ui package never sees any of it beyond a resolved ui.Viewer.
type uiGate struct {
	sessions     *sessionStore
	reveals      *revealStore
	tokens       auth.TokenLookup
	verifier     *auth.Verifier
	requireLogin bool
	log          *slog.Logger
	ui           *ui.Handlers
}

// resolve re-checks the session's token on every request (an indexed
// lookup, no argon2id), so revoking or expiring a token ends its browser
// sessions on their very next request.
func (g *uiGate) resolve(r *http.Request) requestViewer {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return requestViewer{}
	}
	sess, ok := g.sessions.get(c.Value)
	if !ok {
		return requestViewer{}
	}
	tok, err := g.tokens.GetToken(r.Context(), sess.tokenID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			g.sessions.delete(sess.id)
		}
		return requestViewer{}
	}
	now := time.Now()
	if !tok.Active(now) {
		g.sessions.delete(sess.id)
		return requestViewer{}
	}
	if g.verifier.LastUsed != nil {
		g.verifier.LastUsed.Record(tok.ID, now)
	}
	return requestViewer{token: tok, session: sess}
}

// uiViewer adapts the resolved identity for templates. The signed-in token's
// own name and role are shown to whoever holds it; other tokens' names are
// only ever shown to admins, on admin-gated pages.
func (g *uiGate) uiViewer(r *http.Request) ui.Viewer {
	v := ui.Viewer{LoginAvailable: true, ReadAll: !g.requireLogin}
	rv := viewerFrom(r.Context())
	if rv.token == nil {
		return v
	}
	tok := rv.token
	v.Authenticated = true
	v.CSRF = rv.session.csrf
	v.Readable = tok.CanRead
	v.Name = tok.Name
	v.Role = viewerRole(*tok)
	if tok.IsAdmin() {
		v.Admin = true
		v.ReadAll = true
	}
	return v
}

// viewerRole labels what the signed-in token can do, for the sidebar.
func viewerRole(tok domain.Token) string {
	if tok.IsAdmin() {
		return "Admin"
	}
	role := "No access"
	for _, gr := range tok.Grants {
		switch gr.Permission {
		case domain.PermModulePublish, domain.PermModuleAll:
			return "Publisher"
		case domain.PermModuleRead:
			role = "Reader"
		}
	}
	return role
}

// page wraps a browse-UI route: it resolves the viewer and, when sign-in is
// required, sends anonymous visitors to the login page first.
func (g *uiGate) page(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rv := g.resolve(r)
		if rv.token == nil && g.requireLogin {
			g.redirectToLogin(w, r)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), viewerContextKey{}, rv)))
	}
}

// admin wraps a Tokens route. Anyone but an admin session gets 404, so the
// section's existence isn't disclosed; state-changing requests from admins
// must also be same-origin and carry the session's CSRF value.
func (g *uiGate) admin(next http.HandlerFunc) http.HandlerFunc {
	return g.page(func(w http.ResponseWriter, r *http.Request) {
		rv := viewerFrom(r.Context())
		if rv.token == nil || !rv.token.IsAdmin() {
			g.ui.NotFound(w, r)
			return
		}
		if isStateChanging(r.Method) && !g.checkCSRF(w, r, rv.session) {
			return
		}
		next(w, r)
	})
}

func (g *uiGate) checkCSRF(w http.ResponseWriter, r *http.Request, sess session) bool {
	if !sameOrigin(r) {
		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUIFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return false
	}
	presented := r.Header.Get(csrfHeaderName)
	if presented == "" {
		presented = r.PostForm.Get(csrfFieldName)
	}
	if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(sess.csrf)) != 1 {
		http.Error(w, "forbidden: invalid or missing CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

func isStateChanging(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// sameOrigin requires an Origin — or, when a browser withholds it, a
// Referer — whose host matches the request's Host. Browsers send Origin on
// every form POST, so a request with neither didn't come from our pages.
func sameOrigin(r *http.Request) bool {
	source := r.Header.Get("Origin")
	if source == "" || source == "null" {
		source = r.Header.Get("Referer")
	}
	if source == "" {
		return false
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// safeNext accepts only a same-origin path to return to after sign-in, and
// falls back to "/" for anything else: absolute and scheme-relative URLs,
// backslash tricks browsers normalize into "//", and control characters
// browsers strip (which can turn "/\t/evil" into "//evil").
func safeNext(next string) string {
	if next == "" || len(next) > 2048 || next[0] != '/' || strings.HasPrefix(next, "//") {
		return "/"
	}
	for i := 0; i < len(next); i++ {
		if c := next[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	if u.Path == "/login" || u.Path == "/logout" {
		return "/"
	}
	return next
}

func (g *uiGate) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if next := safeNext(r.URL.RequestURI()); next != "/" {
			target += "?next=" + url.QueryEscape(next)
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (g *uiGate) loginPage(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if rv := g.resolve(r); rv.token != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	g.ui.RenderLogin(w, r, ui.LoginPage{Next: next}, http.StatusOK)
}

// loginSubmit verifies a pasted token through the same Verifier as bearer
// authentication — rate limiting, argon2id semaphore, expiry, revocation —
// and then starts a session. The token itself is never stored in the browser.
func (g *uiGate) loginSubmit(w http.ResponseWriter, r *http.Request) {
	// Session-less, so no CSRF value yet: the origin check stops a hostile
	// page from signing a visitor into an attacker's token.
	if !sameOrigin(r) {
		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	next := safeNext(r.PostForm.Get("next"))

	tok, err := g.verifier.AuthenticateToken(r, r.PostForm.Get("token"))
	if err != nil {
		status := http.StatusUnauthorized
		msg := "That token isn't valid. Check that you pasted all of it and that it hasn't expired or been revoked."
		switch {
		case errors.Is(err, domain.ErrRateLimited):
			status = http.StatusTooManyRequests
			msg = "Too many sign-in attempts. Wait a minute and try again."
		case !errors.Is(err, domain.ErrUnauthorized):
			status = http.StatusInternalServerError
			msg = "Sign-in failed on the server. Try again, or check the server log (correlation id: " +
				logging.CorrelationID(r.Context()) + ")."
		}
		logging.Audit(g.log, "ui_login_failed", "client_ip", auth.ClientIP(r, g.verifier.TrustedProxies))
		g.ui.RenderLogin(w, r, ui.LoginPage{Next: next, Error: msg}, status)
		return
	}

	// Always issue a fresh session id at sign-in so a pre-set cookie can't
	// be fixated onto an authenticated session.
	if c, err := r.Cookie(sessionCookieName); err == nil {
		g.sessions.delete(c.Value)
	}
	sess, err := g.sessions.create(tok.ID)
	if err != nil {
		cid := logging.CorrelationID(r.Context())
		http.Error(w, "internal error (correlation id: "+cid+")", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.id,
		Path:     "/",
		MaxAge:   int(sessionAbsoluteTimeout / time.Second),
		HttpOnly: true,
		// Secure unconditionally: TLS is on by default, a TLS-terminating
		// proxy still presents https to the browser, and browsers treat
		// localhost as a secure context for tls.disabled local evaluation.
		Secure: true,
		// Lax rather than Strict: every state-changing request is already
		// guarded by a CSRF value and an origin check, and Strict would
		// drop the session when a registry link is opened from chat or email.
		SameSite: http.SameSiteLaxMode,
	})
	logging.Audit(g.log, "ui_login", "token_id", tok.ID, "token_name", tok.Name, "kind", string(tok.Kind))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (g *uiGate) logout(w http.ResponseWriter, r *http.Request) {
	if rv := g.resolve(r); rv.token != nil {
		if !g.checkCSRF(w, r, rv.session) {
			return
		}
		g.sessions.delete(rv.session.id)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	target := "/"
	if g.requireLogin {
		target = "/login"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
