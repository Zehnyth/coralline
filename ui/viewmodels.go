package ui

import (
	"net/http"
	"time"

	"coralline/app/domain"
)

// Sidebar entries a page can mark as current.
const (
	navHome       = "home"
	navNamespaces = "namespaces"
	navModules    = "modules"
	navTokens     = "tokens"
)

// basePage is what the layout and shell need on every page. Page view
// models embed it, so templates reach .Viewer, .Host, and .Nav directly.
// Typed page structs (instead of map[string]any) make a misspelt field a
// template execution error caught by tests, not a silently empty value.
type basePage struct {
	Viewer Viewer
	// Host is the registry host as the browser addressed it; used in
	// Terraform source addresses and CLI examples.
	Host string
	// Nav names the sidebar entry marked aria-current.
	Nav string
}

func (h *Handlers) base(r *http.Request, nav string) basePage {
	return basePage{Viewer: h.viewer(r), Host: r.Host, Nav: nav}
}

// homePage is the overview dashboard. Every number counts only what the
// viewer can read.
type homePage struct {
	basePage
	NamespaceCount int
	ModuleCount    int
	VersionCount   int
	ProviderCount  int
	// Recent is the most recently published modules, newest first.
	Recent []moduleSummary
}

type errorPage struct {
	basePage
	Status        int
	Heading       string
	Message       string
	CorrelationID string
}

// catalogProps drives the catalog component: the search/filter/sort
// toolbar, the list/grid toggle, the filtered-result line, and the
// no-results state. The items themselves are supplied by the caller.
type catalogProps struct {
	// Singular and Plural name what is listed ("module", "modules").
	Singular string
	Plural   string
	// Placeholder is the search field's placeholder text.
	Placeholder string
	// Providers fills the provider filter, grouped by category; empty
	// hides it.
	Providers []providerGroup
	// Sortable shows the sort select (by name, or most recently updated).
	Sortable bool
	// Views shows the list/grid toggle.
	Views bool
	Total int
}

// moduleCatalog is the searchable module list shared by /modules and
// /n/{ns}.
type moduleCatalog struct {
	catalogProps
	Modules []moduleSummary
	// ShowNamespace shows each module's namespace (the cross-namespace
	// listing needs it; inside a namespace it would repeat the heading).
	ShowNamespace bool
	EmptyTitle    string
	EmptyBody     string
}

type namespaceCard struct {
	domain.Namespace
	ModuleCount int
	Providers   []string
}

type namespacesPage struct {
	basePage
	Catalog    catalogProps
	Namespaces []namespaceCard
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

// versionRow is one published version of a module.
type versionRow struct {
	Version     string
	PublishedAt time.Time
	Prerelease  bool
}

type modulePage struct {
	basePage
	Namespace, Name, System string
	// Versions is newest first.
	Versions []versionRow
	// LatestVersion is the default version: the newest stable release, or
	// the newest version when every release is a pre-release.
	LatestVersion string
	// LatestIsStable decides between the "Recommended" and "Latest" labels.
	LatestIsStable      bool
	SelectedVersion     string
	SelectedPublishedAt time.Time
	Source              string
	Snippet             string
	Publisher           string
}
