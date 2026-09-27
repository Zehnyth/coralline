package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"coralline/app/domain"
)

// maxFormBytes bounds every token form submission.
const maxFormBytes = 64 << 10

// TokenAdmin is everything the Tokens section can do. The implementation in
// app/httpserver takes the acting admin from the request context — never
// from form input — validates through auth.MintToken, and writes audit
// events. There is deliberately no way to request an admin-kind token.
type TokenAdmin interface {
	ListTokens(ctx context.Context, filter domain.TokenFilter) ([]domain.Token, error)
	GetToken(ctx context.Context, id string) (*domain.Token, error)
	CreateStandardToken(ctx context.Context, in NewToken) (*domain.Token, string, error)
	ReplaceTags(ctx context.Context, id string, tags []domain.Tag) error
	ReplaceGrants(ctx context.Context, id string, grants []domain.GrantInput) error
	RevokeToken(ctx context.Context, id string) error
}

// NewToken is a standard-token request from the create form.
type NewToken struct {
	Name      string
	Grants    []domain.GrantInput
	Tags      []domain.Tag
	ExpiresIn time.Duration
}

// savedNotices maps the ?saved= value set by post-redirect-get to a fixed
// message; query text is never reflected into the page.
var savedNotices = map[string]string{
	"grants":    "Access saved.",
	"tags":      "Tags saved.",
	"revoked":   "Token revoked.",
	"duplicate": "That form was already submitted, so no new token was created.",
}

// requireTokenAdmin answers 404 to anyone but an admin, so the section's
// existence is never disclosed. app/httpserver already gates these routes;
// this is defense in depth.
func (h *Handlers) requireTokenAdmin(w http.ResponseWriter, r *http.Request) bool {
	if h.TokenAdmin == nil || !h.viewer(r).Admin {
		h.NotFound(w, r)
		return false
	}
	return true
}

func (h *Handlers) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		h.badRequest(w, r)
		return false
	}
	return true
}

// TokenList renders "/tokens". Search and the status filter run in the
// browser (the catalog component), over every token.
func (h *Handlers) TokenList(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) {
		return
	}
	toks, err := h.TokenAdmin.ListTokens(r.Context(), domain.TokenFilter{})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	now := time.Now()
	rows := make([]tokenRow, 0, len(toks))
	for _, t := range toks {
		rows = append(rows, newTokenRow(t, now))
	}
	h.render(w, r, "tokens", tokensPage{
		basePage:   h.base(r, navTokens),
		Catalog:    catalogProps{Singular: "token", Plural: "tokens", Placeholder: "Name or tag", Total: len(rows)},
		Tokens:     rows,
		StatusTabs: statusTabs(rows),
		Notice:     savedNotices[r.URL.Query().Get("saved")],
	})
}

// NewTokenForm renders "/tokens/new".
func (h *Handlers) NewTokenForm(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) {
		return
	}
	h.renderTokenForm(w, r, tokenForm{Role: "reader", Expiry: "720"}, "", http.StatusOK)
}

// renderTokenForm draws the create form with a fresh submission key, so a
// corrected resubmission after an error is a new submission.
func (h *Handlers) renderTokenForm(w http.ResponseWriter, r *http.Request, form tokenForm, formErr string, status int) {
	ns, err := h.Namespaces.ListNamespaces(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	names := make([]string, 0, len(ns))
	for _, n := range ns {
		names = append(names, n.Name)
	}
	// Default to a specific namespace rather than all of them.
	if form.Namespace == "" && len(names) > 0 {
		form.Namespace = names[0]
	}
	if form.Submission, err = newSubmissionKey(); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.renderStatus(w, r, "token_new", tokenFormPage{
		basePage:          h.base(r, navTokens),
		Form:              form,
		Namespaces:        names,
		Roles:             roleOptions(),
		PermissionOptions: permissionOptions(form.Permissions),
		ExpiryOptions:     expiryOptions(maxExpiryHours),
		MaxExpiryHours:    maxExpiryHours,
		Error:             formErr,
	}, status)
}

// CreateToken handles POST "/tokens". It never renders the secret itself:
// it mints, stashes the plaintext in the session-bound RevealStore, and
// redirects to the one-time reveal page, so reloading the result can't mint
// again and the secret never appears in a URL. A repeated submission key
// lands on the first request's reveal page instead of minting twice.
func (h *Handlers) CreateToken(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) || !h.parseForm(w, r) {
		return
	}
	if h.Reveals == nil {
		h.serverError(w, r, errors.New("token reveal store is not configured"))
		return
	}
	form := tokenForm{
		Name:           r.PostForm.Get("name"),
		Namespace:      r.PostForm.Get("namespace"),
		Role:           r.PostForm.Get("role"),
		Permissions:    r.PostForm["permission"],
		TagsText:       r.PostForm.Get("tags"),
		Expiry:         r.PostForm.Get("expiry"),
		ExpiresInHours: strings.TrimSpace(r.PostForm.Get("expires_in_hours")),
	}
	key := r.PostForm.Get("_submission")
	if key == "" || len(key) > maxSubmissionKeyLen {
		h.renderTokenForm(w, r, form, "This form expired. Check the details and submit it again.", http.StatusBadRequest)
		return
	}
	if id, fresh := h.Reveals.Claim(r, key); !fresh {
		if id != "" {
			http.Redirect(w, r, createdURL(id), http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/tokens?saved=duplicate", http.StatusSeeOther)
		}
		return
	}

	in, err := form.newToken()
	var tok *domain.Token
	var plaintext string
	if err == nil {
		tok, plaintext, err = h.TokenAdmin.CreateStandardToken(r.Context(), in)
	}
	if err != nil {
		h.Reveals.Abandon(r, key)
		if errors.Is(err, domain.ErrInvalid) {
			h.renderTokenForm(w, r, form, userMessage(err), http.StatusBadRequest)
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.Reveals.Complete(r, key, tok.ID, plaintext)
	http.Redirect(w, r, createdURL(tok.ID), http.StatusSeeOther)
}

func createdURL(id string) string {
	return "/tokens/" + url.PathEscape(id) + "/created"
}

// TokenCreated renders "/tokens/{id}/created": the secret the first time,
// and an explanation (never the secret) on every later visit.
func (h *Handlers) TokenCreated(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) {
		return
	}
	tok, ok := h.loadToken(w, r)
	if !ok {
		return
	}
	plaintext := ""
	if h.Reveals != nil {
		plaintext, _ = h.Reveals.Take(r, tok.ID)
	}
	h.render(w, r, "token_created", tokenCreatedPage{
		basePage:   h.base(r, navTokens),
		Token:      newTokenRow(*tok, time.Now()),
		Plaintext:  plaintext,
		EnvVar:     terraformEnvVar(r.Host),
		CanPublish: canPublish(*tok),
	})
}

type tokenDetailState struct {
	Notice        string
	Error         string
	SubmittedTags *string           // re-show what was typed after a rejected tag edit
	Levels        map[string]string // re-show the chosen access levels after a rejected edit
}

// TokenDetail renders "/tokens/{id}".
func (h *Handlers) TokenDetail(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) {
		return
	}
	tok, ok := h.loadToken(w, r)
	if !ok {
		return
	}
	h.renderTokenDetail(w, r, tok, tokenDetailState{Notice: savedNotices[r.URL.Query().Get("saved")]}, http.StatusOK)
}

func (h *Handlers) loadToken(w http.ResponseWriter, r *http.Request) (*domain.Token, bool) {
	tok, err := h.TokenAdmin.GetToken(r.Context(), r.PathValue("id"))
	if errors.Is(err, domain.ErrNotFound) {
		h.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		h.serverError(w, r, err)
		return nil, false
	}
	return tok, true
}

func (h *Handlers) renderTokenDetail(w http.ResponseWriter, r *http.Request, tok *domain.Token, st tokenDetailState, status int) {
	ns, err := h.Namespaces.ListNamespaces(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	tagsText := tagLines(tok.Tags)
	if st.SubmittedTags != nil {
		tagsText = *st.SubmittedTags
	}
	row := newTokenRow(*tok, time.Now())
	h.renderStatus(w, r, "token_detail", tokenDetailPage{
		basePage:       h.base(r, navTokens),
		Token:          row,
		Access:         buildAccessRows(ns, tok.Grants, st.Levels),
		TagsText:       tagsText,
		Notice:         st.Notice,
		Error:          st.Error,
		GrantsEditable: !row.IsAdmin && row.Status == statusActive,
		TagsEditable:   !row.IsAdmin,
		Revocable:      !row.IsAdmin && row.Status != statusRevoked,
	}, status)
}

// UpdateTokenGrants handles POST "/tokens/{id}/grants": one access level
// per namespace (access:<ns> = none | read | publish | custom). Only active
// tokens can have their access changed; the form isn't shown for others.
func (h *Handlers) UpdateTokenGrants(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) || !h.parseForm(w, r) {
		return
	}
	levels := map[string]string{}
	for key, values := range r.PostForm {
		if ns, ok := strings.CutPrefix(key, accessFormPrefix); ok && len(values) > 0 {
			levels[ns] = values[0]
		}
	}
	id := r.PathValue("id")
	tok, ok := h.loadToken(w, r)
	if !ok {
		return
	}
	if !tok.IsAdmin() && !tok.Active(time.Now()) {
		h.renderTokenDetail(w, r, tok, tokenDetailState{Error: "This token is no longer active, so its access can't be changed."}, http.StatusBadRequest)
		return
	}
	grants, err := grantsFromLevels(levels, tok.Grants)
	if err == nil {
		err = h.TokenAdmin.ReplaceGrants(r.Context(), id, grants)
	}
	if err != nil {
		h.tokenDetailError(w, r, err, tokenDetailState{Levels: levels})
		return
	}
	redirectSaved(w, r, id, "grants")
}

// UpdateTokenTags handles POST "/tokens/{id}/tags".
func (h *Handlers) UpdateTokenTags(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) || !h.parseForm(w, r) {
		return
	}
	text := r.PostForm.Get("tags")
	tags, err := domain.ParseTagLines(text)
	id := r.PathValue("id")
	if err == nil {
		err = h.TokenAdmin.ReplaceTags(r.Context(), id, tags)
	}
	if err != nil {
		h.tokenDetailError(w, r, err, tokenDetailState{SubmittedTags: &text})
		return
	}
	redirectSaved(w, r, id, "tags")
}

// RevokeToken handles POST "/tokens/{id}/revoke".
func (h *Handlers) RevokeToken(w http.ResponseWriter, r *http.Request) {
	if !h.requireTokenAdmin(w, r) || !h.parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := h.TokenAdmin.RevokeToken(r.Context(), id); err != nil {
		h.tokenDetailError(w, r, err, tokenDetailState{})
		return
	}
	redirectSaved(w, r, id, "revoked")
}

func (h *Handlers) tokenDetailError(w http.ResponseWriter, r *http.Request, err error, st tokenDetailState) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, domain.ErrNotFound):
		h.NotFound(w, r)
		return
	case errors.Is(err, domain.ErrInvalid):
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
	default:
		h.serverError(w, r, err)
		return
	}
	tok, ok := h.loadToken(w, r)
	if !ok {
		return
	}
	st.Error = userMessage(err)
	h.renderTokenDetail(w, r, tok, st, status)
}

func redirectSaved(w http.ResponseWriter, r *http.Request, id, what string) {
	http.Redirect(w, r, "/tokens/"+url.PathEscape(id)+"?saved="+what, http.StatusSeeOther)
}
