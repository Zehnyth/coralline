package ui

import (
	"net/http"

	"coralline/app/domain"
)

// Sidebar entries a page can mark as current.
const (
	navHome       = "home"
	navNamespaces = "namespaces"
	navModules    = "modules"
	navTokens     = "tokens"
)

// Topbar sections.
const (
	sectionBrowse = "Browse"
	sectionTokens = "Tokens"
)

// basePage is what the layout and shell need on every page. Page view
// models embed it, so templates reach .Viewer, .Host, .Nav, and .Section
// directly. Typed page structs (instead of map[string]any) make a misspelt
// field a template execution error caught by tests, not a silently empty
// value.
type basePage struct {
	Viewer Viewer
	// Host is the registry host as the browser addressed it; used in
	// Terraform source addresses and CLI examples.
	Host string
	// Nav names the sidebar entry marked aria-current.
	Nav string
	// Section is the topbar breadcrumb label.
	Section string
}

func (h *Handlers) base(r *http.Request, nav, section string) basePage {
	return basePage{Viewer: h.viewer(r), Host: r.Host, Nav: nav, Section: section}
}

type homePage struct {
	basePage
}

type errorPage struct {
	basePage
	Status        int
	Heading       string
	Message       string
	CorrelationID string
}

// catalogProps drives the catalog component: the search/sort/filter
// toolbar, the result count, and the no-results state. The grid of cards
// itself is supplied by the caller.
type catalogProps struct {
	// Singular and Plural name what is listed ("module", "modules").
	Singular string
	Plural   string
	// Placeholder is the search field's placeholder text.
	Placeholder string
	// Providers fills the provider filter; empty hides it.
	Providers []string
	Total     int
}

// moduleCatalog is the searchable module grid shared by /modules and
// /n/{ns}.
type moduleCatalog struct {
	catalogProps
	Modules []domain.Module
	// SearchNamespace includes the namespace in each card's search text
	// (the cross-namespace listing needs it; a namespace page doesn't).
	SearchNamespace bool
	EmptyTitle      string
	EmptyBody       string
}

type namespaceCard struct {
	domain.Namespace
	ModuleCount int
	Providers   []string
}

type namespacesPage struct {
	basePage
	Catalog       catalogProps
	Namespaces    []namespaceCard
	ModuleCount   int
	ProviderCount int
}

type modulesPage struct {
	basePage
	Catalog moduleCatalog
}

type namespacePage struct {
	basePage
	Namespace string
	Catalog   moduleCatalog
}

type modulePage struct {
	basePage
	Namespace, Name, System string
	Versions                []string
	// LatestVersion is the default version: the newest stable release, or
	// the newest version when every release is a pre-release.
	LatestVersion string
	// LatestIsStable decides between the "Recommended" and "Latest" labels.
	LatestIsStable  bool
	SelectedVersion string
	Source          string
	Snippet         string
	Publisher       string
}
