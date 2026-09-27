package ui

import (
	"fmt"
	"net/http"
)

// Module renders one module's detail page. Unknown modules, unreadable
// namespaces, and unknown ?version= values all answer the same 404.
func (h *Handlers) Module(w http.ResponseWriter, r *http.Request) {
	ns, name, system := r.PathValue("ns"), r.PathValue("name"), r.PathValue("system")
	v := h.viewer(r)
	if !v.CanRead(ns) {
		h.NotFound(w, r)
		return
	}
	versions, err := h.Versions.ListActiveVersions(r.Context(), ns, name, system)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(versions) == 0 {
		h.NotFound(w, r)
		return
	}
	sortVersionsDesc(versions)
	latest, stable := latestStable(versions)

	selected := r.URL.Query().Get("version")
	if selected == "" {
		selected = latest
	}
	if !contains(versions, selected) {
		h.NotFound(w, r)
		return
	}

	publisher := ""
	if v.Admin && h.Publishers != nil {
		if pub, err := h.Publishers.PublisherName(r.Context(), ns, name, system, selected); err == nil {
			publisher = pub
		}
	}

	source := fmt.Sprintf("%s/%s/%s/%s", r.Host, ns, name, system)
	h.render(w, r, "module", modulePage{
		basePage:  h.base(r, navNamespaces, sectionBrowse),
		Namespace: ns, Name: name, System: system,
		Versions:        versions,
		LatestVersion:   latest,
		LatestIsStable:  stable,
		SelectedVersion: selected,
		Source:          source,
		Snippet:         fmt.Sprintf("module %q {\n  source  = %q\n  version = %q\n}", name, source, selected),
		Publisher:       publisher,
	})
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
