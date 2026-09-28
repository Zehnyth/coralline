package ui

import (
	"net/http"
	"sort"

	"coralline/app/domain"
)

// recentCount is how many modules the overview lists as recently published.
const recentCount = 5

// Home renders the overview dashboard ("/"): counts and the most recently
// published modules, all scoped to what the viewer can read.
func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	ns, err := h.readableNamespaces(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	mods, err := h.readableModules(r, ns, "")
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	versions := 0
	for _, m := range mods {
		versions += m.VersionCount
	}
	h.render(w, r, "home", homePage{
		basePage:       h.base(r, navHome),
		NamespaceCount: len(ns),
		ModuleCount:    len(mods),
		VersionCount:   versions,
		ProviderCount:  len(providersOf(mods)),
		Recent:         recentlyUpdated(mods, recentCount),
	})
}

// readableNamespaces lists only the namespaces the viewer may read, so a
// standard token can't enumerate namespaces it has no grant on.
func (h *Handlers) readableNamespaces(r *http.Request) ([]domain.Namespace, error) {
	all, err := h.Namespaces.ListNamespaces(r.Context())
	if err != nil {
		return nil, err
	}
	v := h.viewer(r)
	out := make([]domain.Namespace, 0, len(all))
	for _, n := range all {
		if v.CanRead(n.Name) {
			out = append(out, n)
		}
	}
	return out, nil
}

// readableModules summarizes every module (in one namespace, or in all of
// them when namespace is "") whose namespace is in readable, sorted by
// namespace and then name.
func (h *Handlers) readableModules(r *http.Request, readable []domain.Namespace, namespace string) ([]moduleSummary, error) {
	allowed := make(map[string]bool, len(readable))
	for _, n := range readable {
		allowed[n.Name] = true
	}
	versions, err := h.Modules.ListPublishedVersions(r.Context(), namespace)
	if err != nil {
		return nil, err
	}
	visible := make([]domain.Version, 0, len(versions))
	for _, v := range versions {
		if allowed[v.Namespace] {
			visible = append(visible, v)
		}
	}
	return summarize(visible), nil
}

// providersOf returns the distinct provider systems of mods, sorted.
func providersOf(mods []moduleSummary) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range mods {
		if !seen[m.System] {
			seen[m.System] = true
			out = append(out, m.System)
		}
	}
	sort.Strings(out)
	return out
}

// Index renders the "/namespaces" listing (kept as the method name for
// history's sake — the field h.Namespaces already claims that identifier).
func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	ns, err := h.readableNamespaces(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	mods, err := h.readableModules(r, ns, "")
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	byNamespace := map[string][]moduleSummary{}
	for _, m := range mods {
		byNamespace[m.Namespace] = append(byNamespace[m.Namespace], m)
	}
	cards := make([]namespaceCard, 0, len(ns))
	for _, n := range ns {
		own := byNamespace[n.Name]
		cards = append(cards, namespaceCard{Namespace: n, ModuleCount: len(own), Providers: providersOf(own)})
	}
	h.render(w, r, "namespaces", namespacesPage{
		basePage: h.base(r, navNamespaces),
		Catalog: catalogProps{
			Singular: "namespace", Plural: "namespaces",
			Placeholder: "Find a namespace", Total: len(cards),
		},
		Namespaces: cards,
	})
}

func newModuleCatalog(mods []moduleSummary, showNamespace bool, emptyTitle, emptyBody string) moduleCatalog {
	return moduleCatalog{
		catalogProps: catalogProps{
			Singular: "module", Plural: "modules",
			Placeholder: "Find a module",
			Providers:   groupProviders(mods),
			Sortable:    true,
			Views:       true,
			Total:       len(mods),
		},
		Modules:       mods,
		ShowNamespace: showNamespace,
		EmptyTitle:    emptyTitle,
		EmptyBody:     emptyBody,
	}
}

// AllModules renders "/modules": every published module across every
// namespace the viewer can read, in one searchable list.
func (h *Handlers) AllModules(w http.ResponseWriter, r *http.Request) {
	ns, err := h.readableNamespaces(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	mods, err := h.readableModules(r, ns, "")
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	// The catalog's default order is by name; namespace breaks ties.
	sort.SliceStable(mods, func(i, j int) bool {
		if mods[i].Name != mods[j].Name {
			return mods[i].Name < mods[j].Name
		}
		return mods[i].Namespace < mods[j].Namespace
	})
	h.render(w, r, "modules", modulesPage{
		basePage: h.base(r, navModules),
		Catalog: newModuleCatalog(mods, true,
			"No modules published yet", "Open the Publish guide in the sidebar to publish your first module."),
	})
}

// Namespace answers 404 — not 403 — for a namespace the viewer can't read,
// so its existence isn't disclosed.
func (h *Handlers) Namespace(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("ns")
	if !h.viewer(r).CanRead(name) {
		h.NotFound(w, r)
		return
	}
	mods, err := h.readableModules(r, []domain.Namespace{{Name: name}}, name)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, "namespace", namespacePage{
		basePage:  h.base(r, navNamespaces),
		Namespace: name,
		Catalog: newModuleCatalog(mods, false,
			"A space for something useful", "No modules are published in this namespace yet. The Publish guide in the sidebar shows how."),
	})
}
