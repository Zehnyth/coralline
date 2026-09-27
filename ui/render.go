package ui

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"coralline/app/logging"
)

// Template sources. Every page is parsed together with the layout, the
// shell, and every component, so any page may use any component.
const (
	layoutGlob    = "templates/layout.html"
	shellGlob     = "templates/shell/*.html"
	componentGlob = "templates/components/*.html"
	pageDir       = "templates/pages"
)

// parseTemplates builds one template set per page, keyed by the page's
// file name without extension ("module", "token_detail", "login", …).
// Template names are global within a set, so parseTemplates refuses a
// {{define}} that appears in more than one file instead of letting the
// last parse silently win.
func parseTemplates(fsys fs.FS, funcs template.FuncMap) (map[string]*template.Template, error) {
	if err := checkUniqueDefines(fsys); err != nil {
		return nil, err
	}
	pages, err := fs.Glob(fsys, pageDir+"/*.html")
	if err != nil {
		return nil, err
	}
	sets := make(map[string]*template.Template, len(pages))
	for _, file := range pages {
		name := strings.TrimSuffix(path.Base(file), ".html")
		t, err := template.New("layout").Funcs(funcs).ParseFS(fsys, layoutGlob, shellGlob, componentGlob, file)
		if err != nil {
			return nil, fmt.Errorf("parsing %s page: %w", name, err)
		}
		sets[name] = t
	}
	return sets, nil
}

// checkUniqueDefines fails on two files defining the same template name,
// or on two files sharing a basename (html/template keys each parsed file
// by its basename, so the second would replace the first).
func checkUniqueDefines(fsys fs.FS) error {
	definedIn := map[string]string{}
	baseIn := map[string]string{}
	for _, glob := range []string{layoutGlob, shellGlob, componentGlob, pageDir + "/*.html"} {
		files, err := fs.Glob(fsys, glob)
		if err != nil {
			return err
		}
		for _, file := range files {
			base := path.Base(file)
			if other, dup := baseIn[base]; dup {
				return fmt.Errorf("template file name %s is used by both %s and %s", base, other, file)
			}
			baseIn[base] = file
			src, err := fs.ReadFile(fsys, file)
			if err != nil {
				return err
			}
			for _, name := range definedNames(string(src)) {
				if other, dup := definedIn[name]; dup && other != file {
					// Pages each run in their own set, so they may reuse
					// the per-page block names ("title", "styles", "content").
					if !(strings.HasPrefix(other, pageDir) && strings.HasPrefix(file, pageDir)) {
						return fmt.Errorf("template %q is defined in both %s and %s", name, other, file)
					}
				}
				definedIn[name] = file
			}
		}
	}
	return nil
}

// definedNames lists the {{define "name"}} actions in a template source.
func definedNames(src string) []string {
	var names []string
	for {
		i := strings.Index(src, `{{define "`)
		if i < 0 {
			return names
		}
		src = src[i+len(`{{define "`):]
		end := strings.IndexByte(src, '"')
		if end < 0 {
			return names
		}
		names = append(names, src[:end])
		src = src[end:]
	}
}

// render writes a full page (the layout wrapping the page's "content").
// Every HTML page is marked no-store: pages are per-viewer, and a signed-out
// browser must not be able to bring an admin page back with the Back button.
func (h *Handlers) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	h.renderStatus(w, r, page, data, http.StatusOK)
}

func (h *Handlers) renderStatus(w http.ResponseWriter, r *http.Request, page string, data any, status int) {
	h.execute(w, r, page, "layout", data, status)
}

func (h *Handlers) execute(w http.ResponseWriter, r *http.Request, page, name string, data any, status int) {
	set, ok := h.templates[page]
	if !ok {
		h.logError(r, "unknown page template", fmt.Errorf("page %q", page))
		plainServerError(w, r)
		return
	}
	var output bytes.Buffer
	if err := set.ExecuteTemplate(&output, name, data); err != nil {
		h.logError(r, "rendering page", err)
		plainServerError(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}

func (h *Handlers) logError(r *http.Request, msg string, err error) {
	if h.Logger != nil {
		h.Logger.Error(msg, "error", err, "path", r.URL.Path, "correlation_id", logging.CorrelationID(r.Context()))
	}
}

// NotFound renders the styled 404 page inside the app shell. It is also used
// by app/httpserver for unknown routes and for sections the viewer may not
// know exist — the page never says why something is missing.
func (h *Handlers) NotFound(w http.ResponseWriter, r *http.Request) {
	h.errorPage(w, r, http.StatusNotFound, "Page not found",
		"This page doesn't exist, or you don't have access to it.")
}

func (h *Handlers) badRequest(w http.ResponseWriter, r *http.Request) {
	h.errorPage(w, r, http.StatusBadRequest, "That request couldn't be processed",
		"The form was incomplete or malformed. Go back, reload the page, and try again.")
}

func (h *Handlers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	h.logError(r, "request failed", err)
	h.errorPage(w, r, http.StatusInternalServerError, "Something went wrong",
		"The registry couldn't complete this request. Try again in a moment.")
}

func (h *Handlers) errorPage(w http.ResponseWriter, r *http.Request, status int, heading, message string) {
	h.renderStatus(w, r, "error", errorPage{
		basePage:      h.base(r, ""),
		Status:        status,
		Heading:       heading,
		Message:       message,
		CorrelationID: logging.CorrelationID(r.Context()),
	}, status)
}

// plainServerError is the last resort when the error page itself can't be
// rendered.
func plainServerError(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "internal error (correlation id: "+logging.CorrelationID(r.Context())+")", http.StatusInternalServerError)
}
