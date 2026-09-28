package httpserver

import (
	"log/slog"
	"net/http"

	"coralline/app/auth"
	"coralline/app/sqlitestore"
	"coralline/app/transport/admin"
)

// NewAdminServer builds the admin JSON API mux. It is deliberately
// served on its own listener (bound to loopback in cmd/coralline, per
// config AdminInternalAddr) rather than mounted on the public mux —
// namespace creation and admin-token minting are never reachable from
// the public surface. It shares the public listener's Verifier, so
// admin authentication gets the same argon2id semaphore and rate limiting.
func NewAdminServer(db *sqlitestore.DB, verifier *auth.Verifier, logger *slog.Logger) http.Handler {
	h := &admin.Handlers{
		Namespaces: db,
		Tokens:     db,
		Verifier:   verifier,
		Log:        logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /namespaces", h.CreateNamespace)
	mux.HandleFunc("GET /namespaces", h.ListNamespaces)
	mux.HandleFunc("POST /tokens", h.CreateToken)
	mux.HandleFunc("GET /tokens", h.ListTokens)
	mux.HandleFunc("POST /tokens/revoke", h.RevokeTokens)
	mux.HandleFunc("GET /tokens/{id}", h.GetToken)
	mux.HandleFunc("DELETE /tokens/{id}", h.RevokeToken)
	mux.HandleFunc("PUT /tokens/{id}/tags", h.ReplaceTokenTags)
	return mux
}
