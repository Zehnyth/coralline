package ui

import (
	"net/http"
	"sort"

	"coralline/app/domain"
)

// Home renders the landing page ("/") — hero only, no namespace data.
func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "home", homePage{basePage: h.base(r, navHome, sectionBrowse)})
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

// readableModules lists every module whose namespace is in readable, in one
// repository call, sorted by namespace and then name.
func (h *Handlers) readableModules(r *http.Request, readable []domain.Namespace) ([]domain.Module, error) {
	allowed := make(map[string]bool, len(readable))
	for _, n := range readable {
		allowed[n.Name] = true
	}
	all, err := h.Modules.ListModules(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]domain.Module, 0, len(all))
	for _, m := range all {
		if allowed[m.Namespace] {
			out = append(out, m)
		}
	}
	sortModules(out)
	return out, nil
}

func sortModules(mods []domain.Module) {
	sort.SliceStable(mods, func(i, j int) bool {
		if mods[i].Namespace != mods[j].Namespace {
			return mods[i].Namespace < mods[j].Namespace
		}
		if mods[i].Name != mods[j].Name {
			return mods[i].Name < mods[j].Name
		}
		return mods[i].System < mods[j].System
	})
}

// providersOf returns the distinct provider systems of mods, sorted.
func providersOf(mods []domain.Module) []string {
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
	mods, err := h.readableModules(r, ns)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	byNamespace := map[string][]domain.Module{}
	for _, m := range mods {
		byNamespace[m.Namespace] = append(byNamespace[m.Namespace], m)
	}
	cards := make([]namespaceCard, 0, len(ns))
	for _, n := range ns {
		own := byNamespace[n.Name]
		cards = append(cards, namespaceCard{Namespace: n, ModuleCount: len(own), Providers: providersOf(own)})
	}
	h.render(w, r, "namespaces", namespacesPage{
		basePage: h.base(r, navNamespaces, sectionBrowse),
		Catalog: catalogProps{
			Singular: "namespace", Plural: "namespaces",
			Placeholder: "Find a namespace…", Total: len(cards),
		},
		Namespaces:    cards,
		ModuleCount:   len(mods),
		ProviderCount: len(providersOf(mods)),
	})
}

func newModuleCatalog(mods []domain.Module, searchNamespace bool, emptyTitle, emptyBody string) moduleCatalog {
	return moduleCatalog{
		catalogProps: catalogProps{
			Singular: "module", Plural: "modules",
			Placeholder: "Find a module…",
			Providers:   providersOf(mods),
			Total:       len(mods),
		},
		Modules:         mods,
		SearchNamespace: searchNamespace,
		EmptyTitle:      emptyTitle,
		EmptyBody:       emptyBody,
	}
}

// AllModules renders "/modules": every published module across every
// namespace the viewer can read, flattened into one searchable grid.
func (h *Handlers) AllModules(w http.ResponseWriter, r *http.Request) {
	ns, err := h.readableNamespaces(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	mods, err := h.readableModules(r, ns)
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
		basePage: h.base(r, navModules, sectionBrowse),
		Catalog: newModuleCatalog(mods, true,
			"No modules published yet.", "Publish your first module to see it listed here."),
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
	mods, err := h.Modules.ListModulesInNamespace(r.Context(), name)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	sortModules(mods)
	h.render(w, r, "namespace", namespacePage{
		basePage:  h.base(r, navNamespaces, sectionBrowse),
		Namespace: name,
		Catalog: newModuleCatalog(mods, false,
			"A space for something useful.", "No modules published in this namespace yet."),
	})
}
