// Package terraform implements the Terraform/OpenTofu module registry
// protocol, verified against HashiCorp's published reference (PRD §5.1,
// FR-1.1–FR-1.9). This is a thin transport adapter: it parses the
// registry-protocol request shape and calls straight into the read
// repository or the publish coordinator — no business logic lives here.
package terraform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"coralline/app/auth"
	"coralline/app/domain"
	"coralline/app/logging"
	"coralline/app/modules"
)

type Handlers struct {
	Reads      modules.ReadRepo
	Publisher  *modules.Publisher
	Verifier   *auth.Verifier
	Attributor interface {
		Record(domain.DownloadEvent)
	}
	// ArchiveReader opens the stored tarball for streaming to the
	// client (used by ServeArchive in publish.go). Kept as a function
	// value rather than a direct storage.BlobStore field so this
	// package doesn't need to import storage just to open a reader.
	ArchiveReader func(ctx context.Context, namespace, name, system, version string) (io.ReadCloser, error)
	PublicRead    bool
	MaxUpload     int64
	// Signer, when set, makes the archive URL Download returns
	// self-authorizing for a few minutes (see ArchiveSigner). Without it,
	// the archive needs a bearer token, which Terraform never sends there.
	Signer *ArchiveSigner
}

func copyBuffered(w io.Writer, r io.Reader) (int64, error) {
	return io.Copy(w, r)
}

// Discovery serves /.well-known/terraform.json (FR-1.1). Note this
// endpoint itself is expected to be reachable only over HTTPS per the
// remote service discovery protocol — see tlsutil package doc comment.
func (h *Handlers) Discovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"modules.v1": "/v1/modules/",
	})
}

// versionsResponse matches the spec's required shape exactly (FR-1.2):
// a single-element "modules" array with the requested module first,
// even though Terraform itself only reads element zero.
type versionsResponse struct {
	Modules []moduleVersions `json:"modules"`
}
type moduleVersions struct {
	Versions []versionEntry `json:"versions"`
}
type versionEntry struct {
	Version string `json:"version"`
}

// ListVersions implements GET /v1/modules/:ns/:name/:system/versions.
// No server-side pre-release filtering (FR-1.3) — every published
// version is returned, and Terraform's own constraint engine decides
// what a given constraint matches.
func (h *Handlers) ListVersions(w http.ResponseWriter, r *http.Request) {
	ns, name, system := r.PathValue("ns"), r.PathValue("name"), r.PathValue("system")

	versions, err := h.Reads.ListActiveVersions(r.Context(), ns, name, system)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(versions) == 0 {
		http.NotFound(w, r) // FR-1.9: no versions => no module, per spec
		return
	}

	entries := make([]versionEntry, 0, len(versions))
	for _, v := range versions {
		entries = append(entries, versionEntry{Version: v})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versionsResponse{
		Modules: []moduleVersions{{Versions: entries}},
	})
}

// Download implements GET /v1/modules/:ns/:name/:system/:version/download
// (FR-1.4/FR-1.5). Per spec this returns 204 with X-Terraform-Get, and
// Content-Type: application/json even though the body is empty — easy
// to miss, explicitly called out in the PRD after checking the spec
// directly.
func (h *Handlers) Download(w http.ResponseWriter, r *http.Request) {
	ns, name, system, version := r.PathValue("ns"), r.PathValue("name"), r.PathValue("system"), r.PathValue("version")

	v, err := h.Reads.GetActiveVersion(r.Context(), ns, name, system, version)
	if err != nil {
		writeError(w, r, err)
		return
	}

	// Relative URL back to our own archive-serving endpoint, with the
	// explicit ?archive=tar.gz go-getter needs since the path itself
	// carries no recognizable extension (FR-1.5 / FR-1.6).
	archivePath := fmt.Sprintf("/v1/modules/%s/%s/%s/%s/archive", ns, name, system, version)
	query := url.Values{"archive": {"tar.gz"}}
	if h.Signer != nil {
		for k, v := range h.Signer.Sign(archivePath) {
			query[k] = v
		}
	}
	getURL := archivePath + "?" + query.Encode()

	if tok := auth.TokenFromContext(r.Context()); h.Attributor != nil {
		tokenID := ""
		if tok != nil {
			tokenID = tok.ID
		}
		h.Attributor.Record(domain.DownloadEvent{
			Namespace: ns, Name: name, System: system, Version: version,
			TokenID: tokenID, At: time.Now().UTC(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Terraform-Get", getURL)
	w.WriteHeader(http.StatusNoContent)
	_ = v // v is validated to exist; nothing further needed in the 204 body
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, domain.ErrUnauthorized):
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case errors.Is(err, domain.ErrConflict):
		http.Error(w, "conflict", http.StatusConflict)
	case errors.Is(err, domain.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, domain.ErrRateLimited):
		http.Error(w, "too many requests", http.StatusTooManyRequests)
	default:
		cid := logging.CorrelationID(r.Context())
		http.Error(w, fmt.Sprintf("internal error (correlation id: %s)", cid), http.StatusInternalServerError)
	}
}
