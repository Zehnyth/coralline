package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// The tests in this file guard the UI's structural conventions: unique
// template names, one script per data-component, and asset caching.

func TestTemplateDefinesAreUnique(t *testing.T) {
	if err := checkUniqueDefines(templatesFS); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		files fstest.MapFS
		want  string
	}{
		"pages may share block names": {fstest.MapFS{
			"templates/pages/one.html": {Data: []byte(`{{define "content"}}{{end}}`)},
			"templates/pages/two.html": {Data: []byte(`{{define "content"}}{{end}}`)},
		}, ""},
		"duplicate component name": {fstest.MapFS{
			"templates/components/a.html": {Data: []byte(`{{define "card"}}a{{end}}`)},
			"templates/components/b.html": {Data: []byte(`{{define "card"}}b{{end}}`)},
		}, `"card"`},
		"component shadowing a page block": {fstest.MapFS{
			"templates/components/a.html": {Data: []byte(`{{define "content"}}{{end}}`)},
			"templates/pages/one.html":    {Data: []byte(`{{define "content"}}{{end}}`)},
		}, `"content"`},
		"duplicate file name": {fstest.MapFS{
			"templates/components/one.html": {Data: []byte(`{{define "one"}}{{end}}`)},
			"templates/pages/one.html":      {Data: []byte(`{{define "content"}}{{end}}`)},
		}, "one.html"},
	} {
		err := checkUniqueDefines(tc.files)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one mentioning %s", name, err, tc.want)
		}
	}
}

var (
	templateComment = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)
	componentAttr   = regexp.MustCompile(`data-component="([^"{}]+)"`)
)

// TestEveryComponentHasAScript ties the two halves of the component
// contract together: each name used in data-component has
// static/js/components/<name>.js, and every script there is used.
func TestEveryComponentHasAScript(t *testing.T) {
	used := map[string]string{}
	err := fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := fs.ReadFile(templatesFS, p)
		if err != nil {
			return err
		}
		for _, m := range componentAttr.FindAllStringSubmatch(templateComment.ReplaceAllString(string(src), ""), -1) {
			for _, name := range strings.Fields(m[1]) {
				used[name] = p
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scripts, err := fs.Glob(staticFS, "static/js/components/*.js")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, s := range scripts {
		have[strings.TrimSuffix(path.Base(s), ".js")] = true
	}
	for name, where := range used {
		if !have[name] {
			t.Errorf("%s uses data-component=%q but static/js/components/%s.js does not exist", where, name, name)
		}
	}
	var unused []string
	for name := range have {
		if _, ok := used[name]; !ok {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("component scripts not used by any template: %v", unused)
	}
}

func testAssets(t *testing.T) *Assets {
	t.Helper()
	a, err := NewAssets(fstest.MapFS{
		"css/base.css":         {Data: []byte("/*base*/")},
		"css/layout.css":       {Data: []byte("/*layout*/")},
		"css/components/b.css": {Data: []byte("/*b*/")},
		"css/components/a.css": {Data: []byte("/*a*/")},
		"css/pages/home.css":   {Data: []byte("/*home*/")},
		"js/app.js":            {Data: []byte("import './core/x.js';")},
		"js/core/x.js":         {Data: []byte("export {};")},
		"img/favicon.svg":      {Data: []byte("<svg/>")},
		".hidden":              {Data: []byte("no")},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func serve(a *Assets, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, req)
	return rr
}

func TestAssetsFingerprintAndCaching(t *testing.T) {
	a := testAssets(t)
	versioned := a.URL("js/app.js")
	if !strings.HasPrefix(versioned, "/static/"+a.fingerprint+"/") || len(a.fingerprint) != fingerprintLen {
		t.Fatalf("URL = %q", versioned)
	}

	rr := serve(a, versioned, nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Cache-Control"), "immutable") ||
		!strings.HasPrefix(rr.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("versioned asset: %d %q %q", rr.Code, rr.Header().Get("Cache-Control"), rr.Header().Get("Content-Type"))
	}

	rr = serve(a, "/static/js/app.js", nil)
	if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "no-cache" || rr.Header().Get("ETag") == "" {
		t.Fatalf("unversioned asset: %d %q etag %q", rr.Code, rr.Header().Get("Cache-Control"), rr.Header().Get("ETag"))
	}
	rr = serve(a, "/static/js/app.js", http.Header{"If-None-Match": {rr.Header().Get("ETag")}})
	if rr.Code != http.StatusNotModified {
		t.Errorf("revalidation: status %d, want 304", rr.Code)
	}

	// A page from the previous release asks for an old fingerprint.
	rr = serve(a, "/static/0123456789ab/js/app.js", nil)
	if rr.Code != http.StatusOK || strings.Contains(rr.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("stale fingerprint: %d %q", rr.Code, rr.Header().Get("Cache-Control"))
	}

	for _, missing := range []string{"/static/nope.css", "/static/.hidden", "/static/", "/static/../go.mod"} {
		if rr := serve(a, missing, nil); rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", missing, rr.Code)
		}
	}
}

func TestAssetsBundleOrder(t *testing.T) {
	a := testAssets(t)
	rr := serve(a, a.URL(bundlePath), nil)
	body := rr.Body.String()
	order := []string{"/*base*/", "/*layout*/", "/*a*/", "/*b*/"}
	last := -1
	for _, marker := range order {
		i := strings.Index(body, marker)
		if i <= last {
			t.Fatalf("bundle order wrong at %s:\n%s", marker, body)
		}
		last = i
	}
	if strings.Contains(body, "/*home*/") {
		t.Error("page stylesheet leaked into the shared bundle")
	}
}

func TestEmbeddedAssetsAreServed(t *testing.T) {
	h := newTestHandlers(t, namespaceRepoStub{}, moduleRepoStub{}, versionRepoStub{}, openViewer)
	for _, p := range []string{bundlePath, "js/app.js", "js/core/component.js", "img/favicon.svg", "css/pages/home.css"} {
		if rr := serve(h.Assets, h.Assets.URL(p), nil); rr.Code != http.StatusOK {
			t.Errorf("%s: status %d", p, rr.Code)
		}
	}
}
