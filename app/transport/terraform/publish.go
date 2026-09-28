package terraform

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"coralline/app/auth"
	"coralline/app/domain"
)

// PublishRequest is Coralline's own publish endpoint — not part of the
// standard Terraform read protocol (which has no publish operation of
// its own), but namespaced under the same API for convenience.
func (h *Handlers) Publish(w http.ResponseWriter, r *http.Request) {
	ns, name, system, version := r.PathValue("ns"), r.PathValue("name"), r.PathValue("system"), r.PathValue("version")

	tok := auth.TokenFromContext(r.Context())
	if tok == nil || !tok.CanPublish(ns) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := domain.ValidateModuleCoordinates(ns, name, system, version); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Bound the request body defensively even though a server-level
	// limit is also applied in httpserver.
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxUpload)

	sha, size, err := h.Publisher.Publish(r.Context(), ns, name, system, version, tok.ID, r.Body)
	if err != nil {
		var maxBytes *http.MaxBytesError
		switch {
		case errors.Is(err, domain.ErrConflict):
			http.Error(w, "version already exists", http.StatusConflict)
		case errors.Is(err, domain.ErrNotFound):
			http.Error(w, "namespace does not exist", http.StatusNotFound)
		case errors.Is(err, domain.ErrTooLarge), errors.As(err, &maxBytes):
			http.Error(w, "archive too large", http.StatusRequestEntityTooLarge)
		default:
			writeError(w, r, err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"namespace": ns, "name": name, "system": system, "version": version,
		"sha256": sha, "size": size,
	})
}

// ServeArchive streams the tarball bytes that the X-Terraform-Get
// header from Download() points at. Proxied directly from the
// filesystem BlobStore in this prototype; an S3 backend would instead
// redirect to a presigned URL rather than proxying the bytes itself.
func (h *Handlers) ServeArchive(w http.ResponseWriter, r *http.Request) {
	ns, name, system, version := r.PathValue("ns"), r.PathValue("name"), r.PathValue("system"), r.PathValue("version")

	v, err := h.Reads.GetActiveVersion(r.Context(), ns, name, system, version)
	if err != nil {
		writeError(w, r, err)
		return
	}

	rc, err := h.ArchiveReader(r.Context(), ns, name, system, version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/gzip")
	// Versions are immutable, so the content hash is a stable validator.
	w.Header().Set("Content-Length", strconv.FormatInt(v.Size, 10))
	w.Header().Set("ETag", `"`+v.SHA256+`"`)
	_, _ = copyBuffered(w, rc)
}
