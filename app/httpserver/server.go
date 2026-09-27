// Package httpserver wires transport handlers, middleware, and the
// health/readiness endpoints into a single net/http server (PRD §5.9,
// §7.4). Routing uses Go 1.22's enhanced ServeMux (method + wildcard
// path patterns) so no third-party router is needed — one less
// dependency, consistent with the "no major dependencies" principle.
package httpserver

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"coralline/app/auth"
	"coralline/app/domain"
	"coralline/app/logging"
	"coralline/app/modules"
	"coralline/app/sqlitestore"
	"coralline/app/storage"
	"coralline/app/transport/terraform"
	"coralline/ui"
)

type Deps struct {
	DB         *sqlitestore.DB
	Blobs      storage.BlobStore
	Publisher  *modules.Publisher
	Verifier   *auth.Verifier
	Attributor *sqlitestore.DownloadAttributor
	Logger     *slog.Logger
	// RequireLogin puts the browse UI behind sign-in and registry reads
	// behind a bearer token. Publishing always requires a token.
	RequireLogin bool
	MaxUpload    int64
}

// New builds the public-facing mux: the Terraform registry protocol, the
// browse UI, and the admin-only Tokens section. The admin JSON API is a
// deliberately separate server (see admin_server.go) bound to loopback
// only — namespace creation and admin-token minting never share this
// listener (PRD FR-4.3 blast-radius note).
func New(d *Deps) (http.Handler, error) {
	anonymousRead := !d.RequireLogin

	tfHandlers := &terraform.Handlers{
		Reads:      d.DB,
		Publisher:  d.Publisher,
		Verifier:   d.Verifier,
		Attributor: d.Attributor,
		PublicRead: anonymousRead,
		MaxUpload:  d.MaxUpload,
		ArchiveReader: func(ctx context.Context, namespace, name, system, version string) (io.ReadCloser, error) {
			return d.Blobs.Get(ctx, namespace+"/"+name+"/"+system+"/"+version+".tar.gz")
		},
	}

	webHandlers, err := ui.NewHandlers(d.DB, d.DB, d.DB)
	if err != nil {
		return nil, err
	}
	sessions := newSessionStore()
	gate := &uiGate{
		sessions:     sessions,
		reveals:      newRevealStore(sessions),
		tokens:       d.DB,
		verifier:     d.Verifier,
		requireLogin: d.RequireLogin,
		log:          d.Logger,
		ui:           webHandlers,
	}
	webHandlers.Viewer = gate.uiViewer
	webHandlers.Publishers = d.DB
	webHandlers.TokenAdmin = &tokenAdmin{db: d.DB, log: d.Logger}
	webHandlers.Reveals = gate.reveals
	webHandlers.Logger = d.Logger

	mux := http.NewServeMux()

	// Discovery is left ungated in this prototype: it carries no
	// sensitive content (just the modules.v1 base path). Real
	// Terraform installations with host credentials configured will
	// send them on every request to this hostname regardless.
	mux.HandleFunc("GET /.well-known/terraform.json", tfHandlers.Discovery)

	readCheck := func(r *http.Request, t *domain.Token) bool { return t.CanRead(r.PathValue("ns")) }
	publishCheck := func(r *http.Request, t *domain.Token) bool { return t.CanPublish(r.PathValue("ns")) }

	mux.HandleFunc("GET /v1/modules/{ns}/{name}/{system}/versions",
		d.Verifier.RequireScope(anonymousRead, readCheck, tfHandlers.ListVersions))
	mux.HandleFunc("GET /v1/modules/{ns}/{name}/{system}/{version}/download",
		d.Verifier.RequireScope(anonymousRead, readCheck, tfHandlers.Download))
	mux.HandleFunc("GET /v1/modules/{ns}/{name}/{system}/{version}/archive",
		d.Verifier.RequireScope(anonymousRead, readCheck, tfHandlers.ServeArchive))
	mux.HandleFunc("PUT /v1/modules/{ns}/{name}/{system}/{version}/publish",
		d.Verifier.RequireScope(false, publishCheck, tfHandlers.Publish)) // publish is never anonymous, regardless of require_login

	// Sign-in and sign-out are never gated.
	mux.HandleFunc("GET /login", gate.loginPage)
	mux.HandleFunc("POST /login", gate.loginSubmit)
	mux.HandleFunc("POST /logout", gate.logout)

	// Browse UI: sign-in required when require_login is on; listings are
	// scoped to the namespaces the viewer's token can read.
	mux.HandleFunc("GET /{$}", gate.page(webHandlers.Home))
	mux.HandleFunc("GET /namespaces", gate.page(webHandlers.Index))
	mux.HandleFunc("GET /modules", gate.page(webHandlers.AllModules))
	mux.HandleFunc("GET /n/{ns}", gate.page(webHandlers.Namespace))
	mux.HandleFunc("GET /n/{ns}/{name}/{system}", gate.page(webHandlers.Module))

	// Tokens: admin sessions only; everyone else gets 404.
	mux.HandleFunc("GET /tokens", gate.admin(webHandlers.TokenList))
	mux.HandleFunc("GET /tokens/new", gate.admin(webHandlers.NewTokenForm))
	mux.HandleFunc("POST /tokens", gate.admin(webHandlers.CreateToken))
	mux.HandleFunc("GET /tokens/{id}", gate.admin(webHandlers.TokenDetail))
	mux.HandleFunc("GET /tokens/{id}/created", gate.admin(webHandlers.TokenCreated))
	mux.HandleFunc("POST /tokens/{id}/grants", gate.admin(webHandlers.UpdateTokenGrants))
	mux.HandleFunc("POST /tokens/{id}/tags", gate.admin(webHandlers.UpdateTokenTags))
	mux.HandleFunc("POST /tokens/{id}/revoke", gate.admin(webHandlers.RevokeToken))

	// Static assets stay public: the login page needs its stylesheets. URLs
	// carry a content fingerprint, so they are served as immutable.
	mux.Handle("GET /static/", webHandlers.Assets)

	// Anything else a browser asks for gets the styled 404 inside the app
	// shell (after sign-in, so it reveals nothing); registry protocol paths
	// keep a plain 404 for API clients.
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/.well-known/") {
			http.NotFound(w, r)
			return
		}
		gate.page(webHandlers.NotFound)(w, r)
	})

	// Liveness: process is up, no dependency checks (FR-9.1).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// Readiness: SQLite must be openable (FR-9.2).
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := d.DB.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not ready"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	return logging.Middleware(d.Logger)(securityHeaders(mux)), nil
}

// securityHeaders applies browser hardening to every response. Framing is
// denied outright so the admin Tokens pages can't be clickjacked, and the
// referrer policy keeps token-page URLs from leaking to other sites while
// still sending a same-origin Referer for the CSRF origin check fallback.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// NewTLSListener wraps a net.Listener with the given certificate. See
// tlsutil's package doc for the local-evaluation-only caveat this
// carries — terraform init has no way to bypass an untrusted cert.
func NewTLSListener(inner net.Listener, cert tls.Certificate) net.Listener {
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	return tls.NewListener(inner, cfg)
}
