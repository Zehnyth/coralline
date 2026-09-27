// Package admin implements the admin JSON API: namespace and token
// management. Per PRD FR-5.8, mutating CLI subcommands call this API
// over loopback rather than opening the SQLite file directly, since
// SQLite allows only one writer and a second process opening the same
// file produces SQLITE_BUSY races. This also means the CLI has no
// privileged access beyond what any external script calling the same
// API would have.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"coralline/app/auth"
	"coralline/app/domain"
	"coralline/app/logging"
)

// maxRequestBytes bounds every admin request body.
const maxRequestBytes = 1 << 20

// NamespaceRepo and TokenRepo are the narrow slices of sqlitestore.DB
// this handler needs.
type NamespaceRepo interface {
	CreateNamespace(ctx context.Context, name string) error
	NamespaceExists(ctx context.Context, name string) (bool, error)
	ListNamespaces(ctx context.Context) ([]domain.Namespace, error)
}
type TokenRepo interface {
	CreateToken(ctx context.Context, t domain.Token) error
	GetTokenDetail(ctx context.Context, id string) (*domain.Token, error)
	ListTokens(ctx context.Context, filter domain.TokenFilter) ([]domain.Token, error)
	RevokeToken(ctx context.Context, id string) error
	ReplaceTokenTags(ctx context.Context, id string, tags map[string]string) error
}

type Handlers struct {
	Namespaces NamespaceRepo
	Tokens     TokenRepo
	Verifier   *auth.Verifier
	Log        *slog.Logger
}

// mintStore joins the two repos into the auth.TokenStore minting needs.
type mintStore struct {
	TokenRepo
	NamespaceRepo
}

// requireAdmin authenticates through the same Verifier as every other
// token — id lookup, argon2id semaphore, per-IP rate limiting, expiry, and
// revocation — and then requires an admin-kind token.
func (h *Handlers) requireAdmin(w http.ResponseWriter, r *http.Request) (*domain.Token, bool) {
	tok, err := h.Verifier.Authenticate(r)
	switch {
	case errors.Is(err, domain.ErrRateLimited):
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return nil, false
	case err != nil:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	case !tok.IsAdmin():
		http.Error(w, "forbidden: an admin token is required", http.StatusForbidden)
		return nil, false
	}
	return tok, true
}

type grantView struct {
	Namespace  string `json:"namespace"`
	Permission string `json:"permission"`
}

type tokenView struct {
	ID            string            `json:"id"`
	Kind          domain.TokenKind  `json:"kind"`
	Name          string            `json:"name"`
	CreatedBy     string            `json:"created_by,omitempty"`
	CreatedByName string            `json:"created_by_name,omitempty"`
	Grants        []grantView       `json:"grants"`
	Tags          map[string]string `json:"tags"`
	CreatedAt     time.Time         `json:"created_at"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	LastUsedAt    *time.Time        `json:"last_used_at,omitempty"`
	RevokedAt     *time.Time        `json:"revoked_at,omitempty"`
}

func newTokenView(t domain.Token) tokenView {
	v := tokenView{
		ID: t.ID, Kind: t.Kind, Name: t.Name,
		CreatedBy: t.CreatedBy, CreatedByName: t.CreatedByName,
		Grants: []grantView{}, Tags: t.Tags,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
	if v.Tags == nil {
		v.Tags = map[string]string{}
	}
	for _, g := range t.Grants {
		v.Grants = append(v.Grants, grantView{Namespace: g.Namespace, Permission: string(g.Permission)})
	}
	return v
}

type createNamespaceRequest struct {
	Name string `json:"name"`
}

func (h *Handlers) CreateNamespace(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	var req createNamespaceRequest
	if err := decodeJSON(w, r, &req); err != nil || !domain.ValidNamespaceName(req.Name) {
		http.Error(w, "invalid namespace name", http.StatusBadRequest)
		return
	}
	if err := h.Namespaces.CreateNamespace(r.Context(), req.Name); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			http.Error(w, "namespace already exists", http.StatusConflict)
			return
		}
		h.writeError(w, r, err)
		return
	}
	logging.Audit(h.Log, "namespace_created", "namespace", req.Name, "actor", actor.Name, "actor_id", actor.ID)
	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) ListNamespaces(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	ns, err := h.Namespaces.ListNamespaces(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ns)
}

type createTokenRequest struct {
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Grants         []grantView       `json:"grants"`
	Tags           map[string]string `json:"tags"`
	ExpiresInHours int               `json:"expires_in_hours"`
}

// CreateToken mints standard or admin tokens. Admin tokens can only be
// minted here, on the loopback listener — never from the public web UI.
func (h *Handlers) CreateToken(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	var req createTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Kind == "" {
		req.Kind = string(domain.TokenKindStandard)
	}
	if req.ExpiresInHours < 0 || req.ExpiresInHours > int(auth.MaxTokenLifetime/time.Hour) {
		http.Error(w, "expires_in_hours is out of range", http.StatusBadRequest)
		return
	}

	grants := make([]domain.GrantInput, 0, len(req.Grants))
	for _, g := range req.Grants {
		grants = append(grants, domain.GrantInput{Namespace: g.Namespace, Permission: g.Permission})
	}
	tok, plaintext, err := auth.MintToken(r.Context(), mintStore{h.Tokens, h.Namespaces}, auth.MintRequest{
		Kind:      domain.TokenKind(req.Kind),
		Name:      req.Name,
		CreatedBy: actor.ID,
		Grants:    grants,
		Tags:      domain.SortedTags(req.Tags),
		ExpiresIn: time.Duration(req.ExpiresInHours) * time.Hour,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	logging.Audit(h.Log, "token_created",
		"token_id", tok.ID, "token_name", tok.Name, "kind", string(tok.Kind),
		"actor", actor.Name, "actor_id", actor.ID)

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":    tok.ID,
		"name":  tok.Name,
		"kind":  string(tok.Kind),
		"token": plaintext, // shown once, never persisted
	})
}

func (h *Handlers) ListTokens(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	filter, err := domain.ParseTokenFilter(r.URL.Query().Get("tag"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	toks, err := h.Tokens.ListTokens(r.Context(), filter)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := make([]tokenView, 0, len(toks))
	for _, t := range toks {
		out = append(out, newTokenView(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) GetToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	tok, err := h.Tokens.GetTokenDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newTokenView(*tok))
}

func (h *Handlers) RevokeToken(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := h.Tokens.RevokeToken(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	logging.Audit(h.Log, "token_revoked", "token_id", id, "actor", actor.Name, "actor_id", actor.ID)
	w.WriteHeader(http.StatusNoContent)
}

type revokeTokensRequest struct {
	IDs []string `json:"ids"`
}

type revokeFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// RevokeTokens revokes an explicit list of ids under one authentication.
// The CLI's `token revoke --tag k=v --all` lists matches, confirms with the
// operator, then sends exactly the ids it showed — so a token tagged after
// the listing is never revoked unseen. One request also keeps a large
// offboarding from tripping the per-IP authentication rate limit.
func (h *Handlers) RevokeTokens(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	var req revokeTokensRequest
	if err := decodeJSON(w, r, &req); err != nil || len(req.IDs) == 0 || len(req.IDs) > 1000 {
		http.Error(w, "request must list between 1 and 1000 token ids", http.StatusBadRequest)
		return
	}
	revoked := []string{}
	failed := []revokeFailure{}
	for _, id := range req.IDs {
		if err := h.Tokens.RevokeToken(r.Context(), id); err != nil {
			if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
				failed = append(failed, revokeFailure{ID: id, Error: publicMessage(err)})
				continue
			}
			h.writeError(w, r, err)
			return
		}
		logging.Audit(h.Log, "token_revoked", "token_id", id, "actor", actor.Name, "actor_id", actor.ID)
		revoked = append(revoked, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked, "failed": failed})
}

type replaceTagsRequest struct {
	Tags map[string]string `json:"tags"`
}

func (h *Handlers) ReplaceTokenTags(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	var req replaceTagsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	tags, err := domain.NormalizeTags(domain.SortedTags(req.Tags))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	id := r.PathValue("id")
	if err := h.Tokens.ReplaceTokenTags(r.Context(), id, tags); err != nil {
		h.writeError(w, r, err)
		return
	}
	logging.Audit(h.Log, "token_tags_updated", "token_id", id, "actor", actor.Name, "actor_id", actor.ID)
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// publicMessage is the text of an invalid-input or conflict error, which
// is written to be shown to the operator.
func publicMessage(err error) string {
	if errors.Is(err, domain.ErrNotFound) {
		return "not found"
	}
	return err.Error()
}

func (h *Handlers) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, domain.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, domain.ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		cid := logging.CorrelationID(r.Context())
		h.Log.Error("admin request failed", "error", err, "path", r.URL.Path, "correlation_id", cid)
		http.Error(w, "internal error (correlation id: "+cid+")", http.StatusInternalServerError)
	}
}
