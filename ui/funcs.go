package ui

import (
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"time"
	"unicode"
	"unicode/utf8"

	"coralline/app/domain"
)

// funcs is the template function set shared by every page.
func (h *Handlers) funcs() template.FuncMap {
	return template.FuncMap{
		// asset resolves a path under static/ to its cache-busting URL. It
		// reads h.Assets at execution time so the preview can swap it.
		"asset":    func(p string) string { return h.Assets.URL(p) },
		"icon":     icon,
		"dict":     dict,
		"crumbs":   crumbs,
		"plural":   plural,
		"initial":  initial,
		"tone":     tone,
		"provider": providerName,
		"date":     formatDate,
		"version":  func() string { return domain.BuildVersion },
	}
}

// dict builds a map from alternating keys and values, so a template can
// pass named props to a component: {{template "empty-state" dict "Title" "…"}}.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("dict needs key/value pairs")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict key %v is not a string", pairs[i])
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// crumb is one breadcrumbs entry; an empty Href marks the current page.
type crumb struct {
	Label string
	Href  string
}

// crumbs builds a breadcrumb trail from alternating labels and hrefs:
// {{template "breadcrumbs" (crumbs "Namespaces" "/namespaces" .Namespace "")}}.
func crumbs(pairs ...string) ([]crumb, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("crumbs needs label/href pairs")
	}
	out := make([]crumb, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, crumb{Label: pairs[i], Href: pairs[i+1]})
	}
	return out, nil
}

// plural formats a count with the right noun: "1 module", "3 modules".
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, pluralForm)
}

// initial is the upper-cased first character of s, rune-safe.
func initial(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || r == utf8.RuneError {
		return "?"
	}
	return string(unicode.ToUpper(r))
}

// capitalize upper-cases the first character of s, rune-safe.
func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// tone picks one of three avatar colour tones from a name, so a namespace
// keeps its colour however the list is sorted or filtered.
func tone(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return int(h.Sum32() % 3)
}

func providerName(system string) string {
	switch system {
	case "aws":
		return "AWS"
	case "google":
		return "Google Cloud"
	case "azurerm":
		return "Azure"
	default:
		return system
	}
}

// formatDate renders a calendar date in UTC, the zone every timestamp in the
// UI is shown in.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("Jan 2, 2006")
}

// formatTimestamp renders a date and time in UTC.
func formatTimestamp(t time.Time) string {
	return t.UTC().Format("Jan 2, 2006 15:04 UTC")
}
