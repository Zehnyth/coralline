package ui

import (
	"fmt"
	"net/http"
	"time"
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
	published, err := h.Modules.ListPublishedVersions(r.Context(), ns)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	publishedAt := map[string]time.Time{}
	var versions []string
	for _, pv := range published {
		if pv.Namespace == ns && pv.Name == name && pv.System == system {
			versions = append(versions, pv.Version)
			publishedAt[pv.Version] = pv.PublishedAt
		}
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
	if _, ok := publishedAt[selected]; !ok {
		h.NotFound(w, r)
		return
	}

	publisher := ""
	if v.Admin && h.Publishers != nil {
		if pub, err := h.Publishers.PublisherName(r.Context(), ns, name, system, selected); err == nil {
			publisher = pub
		}
	}

	rows := make([]versionRow, 0, len(versions))
	for _, ver := range versions {
		rows = append(rows, versionRow{Version: ver, PublishedAt: publishedAt[ver], Prerelease: isPrerelease(ver)})
	}
	source := fmt.Sprintf("%s/%s/%s/%s", r.Host, ns, name, system)
	h.render(w, r, "module", modulePage{
		basePage:  h.base(r, navNamespaces),
		Namespace: ns, Name: name, System: system,
		Versions:            rows,
		LatestVersion:       latest,
		LatestIsStable:      stable,
		SelectedVersion:     selected,
		SelectedPublishedAt: publishedAt[selected],
		Source:              source,
		Snippet:             fmt.Sprintf("module %q {\n  source  = %q\n  version = %q\n}", name, source, selected),
		Publisher:           publisher,
	})
}
