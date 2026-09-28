// Package ui is the server-rendered browse UI: Go templates plus a small
// set of native ES-module components, with no SPA and no frontend build
// step.
//
// Layout (see README.md for the full component contract):
//
//   - templates/layout.html is the app shell, templates/shell/ holds the
//     pieces only the shell uses (head, sidebar, topbar, dialog, toast),
//     templates/components/ holds reusable components, and
//     templates/pages/ holds one file per page.
//   - static/css/{base,layout}.css plus static/css/components/*.css are
//     served as one bundle (css/app.css); each page adds its own
//     static/css/pages/<page>.css.
//   - static/js/app.js mounts static/js/components/<name>.js onto every
//     element marked data-component="<name>".
//
// The UI never authenticates anyone: app/httpserver resolves the Viewer
// and enforces sign-in, CSRF, and the admin gate before a handler runs.
// Handlers re-check visibility anyway, and fail closed without a Viewer.
package ui

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"coralline/app/domain"
)

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

type NamespaceRepo interface {
	ListNamespaces(ctx context.Context) ([]domain.Namespace, error)
}

type ModuleRepo interface {
	// ListPublishedVersions returns every active version with its publish
	// time — across all namespaces when namespace is "" — in one query.
	// Listings, the overview, and module pages are all derived from it.
	ListPublishedVersions(ctx context.Context, namespace string) ([]domain.Version, error)
}

// PublisherRepo resolves who published a version. It is only consulted for
// admin viewers — token names are never shown to anyone else.
type PublisherRepo interface {
	PublisherName(ctx context.Context, namespace, name, system, version string) (string, error)
}

// Viewer describes who is making a request.
type Viewer struct {
	Authenticated bool
	// Admin viewers can see and manage tokens and see token names.
	Admin bool
	// Name is the signed-in token's own name, shown to whoever holds it.
	// Other tokens' names are only ever shown to admins.
	Name string
	// Role labels what the signed-in token can do ("Admin", "Publisher",
	// "Reader").
	Role string
	// CSRF is the per-session value every mutating form must echo back.
	CSRF string
	// LoginAvailable shows a Sign in link to anonymous viewers.
	LoginAvailable bool
	// ReadAll is true when every namespace is visible: anonymous reads are
	// enabled, or the viewer is an admin.
	ReadAll bool
	// Readable reports namespace visibility when ReadAll is false.
	Readable func(namespace string) bool
}

func (v Viewer) CanRead(namespace string) bool {
	if v.ReadAll {
		return true
	}
	return v.Readable != nil && v.Readable(namespace)
}

type Handlers struct {
	Namespaces NamespaceRepo
	Modules    ModuleRepo
	// Viewer resolves the requester. When nil, every request is an anonymous
	// viewer who can read nothing — the UI fails closed.
	Viewer func(r *http.Request) Viewer
	// Publishers is optional; used only for admin viewers.
	Publishers PublisherRepo
	// TokenAdmin backs the admin-only Tokens section; nil disables it.
	TokenAdmin TokenAdmin
	// Reveals holds freshly minted token secrets until they are shown once.
	// Token creation is refused while it is nil.
	Reveals RevealStore
	// Assets serves /static/ and resolves asset URLs in templates. It
	// defaults to the embedded files; the preview swaps in files on disk.
	Assets *Assets
	// Logger records template failures; optional.
	Logger *slog.Logger

	templates map[string]*template.Template
}

// NewHandlers parses every page template and prepares the embedded assets.
func NewHandlers(ns NamespaceRepo, mods ModuleRepo) (*Handlers, error) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	assets, err := NewAssets(static, false)
	if err != nil {
		return nil, fmt.Errorf("preparing static assets: %w", err)
	}
	h := &Handlers{Namespaces: ns, Modules: mods, Assets: assets}
	if h.templates, err = parseTemplates(templatesFS, h.funcs()); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Handlers) viewer(r *http.Request) Viewer {
	if h.Viewer == nil {
		return Viewer{}
	}
	return h.Viewer(r)
}
