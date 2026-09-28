package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// bundlePath is the virtual stylesheet every page loads: base.css, then
// layout.css, then every components/*.css in name order. Authoring stays
// split one file per component; the browser makes one request.
const bundlePath = "css/app.css"

// fingerprintLen is how many hex characters of the content hash go into
// asset URLs.
const fingerprintLen = 12

type asset struct {
	data        []byte
	etag        string
	contentType string
}

// Assets serves the UI's static files under /static/ and hands templates
// their URLs.
//
// In production every URL carries a fingerprint of the whole static tree —
// /static/<fingerprint>/css/app.css — and is served as immutable, so a
// browser fetches each file once per release. ES modules import siblings by
// relative path, so they inherit the fingerprint without knowing about it.
// Unversioned /static/... paths still work (tools, bookmarks) and are served
// with an ETag and Cache-Control: no-cache, so they revalidate cheaply.
//
// In dev mode (the preview) files are re-read from disk on every request,
// URLs carry no fingerprint, and nothing is cached.
type Assets struct {
	fsys        fs.FS
	dev         bool
	fingerprint string
	files       map[string]asset
}

// NewAssets indexes fsys (rooted at the static directory). dev re-reads
// files on every request instead of caching them in memory.
func NewAssets(fsys fs.FS, dev bool) (*Assets, error) {
	a := &Assets{fsys: fsys, dev: dev}
	if dev {
		a.fingerprint = "dev"
		return a, nil
	}
	files, err := loadAssets(fsys)
	if err != nil {
		return nil, err
	}
	a.files = files
	a.fingerprint = treeFingerprint(files)
	return a, nil
}

// URL returns the path a template should link to for a file under static/.
func (a *Assets) URL(p string) string {
	p = strings.TrimPrefix(p, "/")
	if a.dev {
		return "/static/" + p
	}
	return "/static/" + a.fingerprint + "/" + p
}

// ServeHTTP serves GET/HEAD /static/... .
func (a *Assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/static/")
	immutable := false
	if first, rest, ok := strings.Cut(p, "/"); ok && looksLikeFingerprint(first) {
		// A page rendered by the previous release may still ask for the old
		// fingerprint; serve the current file, just not as immutable.
		p = rest
		immutable = !a.dev && first == a.fingerprint
	}
	if p == "" || !fs.ValidPath(p) {
		http.NotFound(w, r)
		return
	}
	f, ok := a.lookup(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	switch {
	case a.dev:
		h.Set("Cache-Control", "no-store")
	case immutable:
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		h.Set("Cache-Control", "no-cache")
	}
	if f.etag != "" {
		h.Set("ETag", f.etag)
	}
	// ServeContent answers If-None-Match with 304 and handles HEAD and ranges.
	http.ServeContent(w, r, path.Base(p), time.Time{}, bytes.NewReader(f.data))
}

func (a *Assets) lookup(p string) (asset, bool) {
	if !a.dev {
		f, ok := a.files[p]
		return f, ok
	}
	var data []byte
	var err error
	if p == bundlePath {
		data, err = buildBundle(a.fsys)
	} else {
		data, err = fs.ReadFile(a.fsys, p)
	}
	if err != nil {
		return asset{}, false
	}
	return asset{data: data, contentType: contentTypeFor(p)}, true
}

func loadAssets(fsys fs.FS) (map[string]asset, error) {
	files := map[string]asset{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		files[p] = newAsset(p, data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	bundle, err := buildBundle(fsys)
	if err != nil {
		return nil, err
	}
	files[bundlePath] = newAsset(bundlePath, bundle)
	return files, nil
}

func newAsset(p string, data []byte) asset {
	sum := sha256.Sum256(data)
	return asset{data: data, etag: `"` + hex.EncodeToString(sum[:])[:16] + `"`, contentType: contentTypeFor(p)}
}

// buildBundle concatenates the shared stylesheets in cascade order.
func buildBundle(fsys fs.FS) ([]byte, error) {
	components, err := fs.Glob(fsys, "css/components/*.css")
	if err != nil {
		return nil, err
	}
	sort.Strings(components)
	var b bytes.Buffer
	for _, p := range append([]string{"css/base.css", "css/layout.css"}, components...) {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("bundling %s: %w", p, err)
		}
		fmt.Fprintf(&b, "/* %s */\n", p)
		b.Write(data)
		if !bytes.HasSuffix(data, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	return b.Bytes(), nil
}

// treeFingerprint hashes every file's path and content.
func treeFingerprint(files map[string]asset) string {
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, p := range names {
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(files[p].data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:fingerprintLen]
}

func looksLikeFingerprint(s string) bool {
	if s == "dev" {
		return true
	}
	if len(s) != fingerprintLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func contentTypeFor(p string) string {
	switch path.Ext(p) {
	case ".js":
		// Module scripts must be served with a JavaScript MIME type, and
		// X-Content-Type-Options: nosniff is set on every response.
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	}
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}
