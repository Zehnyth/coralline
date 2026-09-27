package ui

import (
	"fmt"
	"html/template"
)

type iconShape struct {
	strokeWidth string
	round       bool
	body        string
}

// icons are the UI's inline SVG icons, keyed by name. They are trusted
// constants, never user input, which is what makes returning template.HTML
// from icon safe.
var icons = map[string]iconShape{
	"grid":           {"1.6", false, `<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`},
	"cube":           {"1.5", false, `<path d="m12 2 9 5v10l-9 5-9-5V7zM3 7l9 5 9-5M12 12v10M7.5 4.5l9 5"/>`},
	"search":         {"1.6", false, `<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/>`},
	"copy":           {"1.6", false, `<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M15 8V4H4v11h4"/>`},
	"sun":            {"1.6", false, `<circle cx="12" cy="12" r="4.5"/><path d="M12 2.5v3M12 18.5v3M4.4 4.4l2.1 2.1M17.5 17.5l2.1 2.1M2.5 12h3M18.5 12h3M4.4 19.6l2.1-2.1M17.5 6.5l2.1-2.1"/>`},
	"moon":           {"1.6", false, `<path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a7 7 0 0 0 10.5 10.5z"/>`},
	"collapse":       {"1.8", true, `<path d="M14.5 6.5 9 12l5.5 5.5"/>`},
	"menu":           {"1.6", false, `<path d="M3.5 6.5h17M3.5 12h17M3.5 17.5h17"/>`},
	"close":          {"1.8", true, `<path d="M6 6l12 12M18 6 6 18"/>`},
	"home":           {"1.6", true, `<path d="M4 11.5 12 4l8 7.5"/><path d="M6 10.5V19a1 1 0 0 0 1 1h3v-5.5h4V20h3a1 1 0 0 0 1-1v-8.5"/>`},
	"key":            {"1.6", true, `<circle cx="7.5" cy="14.5" r="3.3"/><path d="M9.8 12.2 18.7 3.3"/><path d="M15.2 6.8l2.3 2.3"/><path d="M17.7 4.3l2.3 2.3"/>`},
	"plus":           {"1.8", true, `<path d="M12 5v14M5 12h14"/>`},
	"lock":           {"1.6", true, `<rect x="5" y="10.5" width="14" height="10" rx="2"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>`},
	"layers":         {"1.6", true, `<path d="m12 3 9 5-9 5-9-5z"/><path d="m3 13 9 5 9-5"/>`},
	"shield":         {"1.6", true, `<path d="M12 3 5 6v5c0 4.5 3 8 7 10 4-2 7-5.5 7-10V6z"/><path d="m9 12 2 2 4-4"/>`},
	"alert":          {"1.6", true, `<circle cx="12" cy="12" r="9"/><path d="M12 7.5v5.5M12 16.5v.01"/>`},
	"warning":        {"1.6", true, `<path d="M10.3 4.2 2.8 17.5A2 2 0 0 0 4.5 20.5h15a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0z"/><path d="M12 9.5v4M12 17v.01"/>`},
	"overview":       {"1.6", true, `<rect x="3.5" y="3.5" width="7" height="9" rx="1.5"/><rect x="13.5" y="3.5" width="7" height="5" rx="1.5"/><rect x="13.5" y="11.5" width="7" height="9" rx="1.5"/><rect x="3.5" y="15.5" width="7" height="5" rx="1.5"/>`},
	"folders":        {"1.6", true, `<path d="M8 4.5h3l2 2h6a1.5 1.5 0 0 1 1.5 1.5v7a1.5 1.5 0 0 1-1.5 1.5H8A1.5 1.5 0 0 1 6.5 15V6A1.5 1.5 0 0 1 8 4.5z"/><path d="M17.5 19.5H5A1.5 1.5 0 0 1 3.5 18V9"/>`},
	"book":           {"1.6", true, `<path d="M4.5 5.5A1.5 1.5 0 0 1 6 4h12.5v13H6a1.5 1.5 0 0 0-1.5 1.5z"/><path d="M4.5 18.5A1.5 1.5 0 0 0 6 20h12.5v-3"/><path d="M9 8h6"/>`},
	"logout":         {"1.6", true, `<path d="M14 7.5V5.5A1.5 1.5 0 0 0 12.5 4h-7A1.5 1.5 0 0 0 4 5.5v13A1.5 1.5 0 0 0 5.5 20h7a1.5 1.5 0 0 0 1.5-1.5v-2"/><path d="M9.5 12h11M17.5 8.5 21 12l-3.5 3.5"/>`},
	"collapse-panel": {"1.8", true, `<path d="M11.5 7 6.5 12l5 5M17.5 7l-5 5 5 5"/>`},
	"list":           {"1.7", true, `<path d="M9 6.5h11M9 12h11M9 17.5h11M4.5 6.5h.01M4.5 12h.01M4.5 17.5h.01"/>`},
	"dots":           {"2", true, `<path d="M5.5 12h.01M12 12h.01M18.5 12h.01"/>`},
	"check":          {"1.8", true, `<path d="m5 12.5 4.5 4.5L19 7.5"/>`},
	"eye":            {"1.6", true, `<path d="M2.5 12s3.5-6.5 9.5-6.5S21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z"/><circle cx="12" cy="12" r="2.8"/>`},
	"ban":            {"1.6", true, `<circle cx="12" cy="12" r="8.5"/><path d="m6 6 12 12"/>`},
	"arrow":          {"1.7", true, `<path d="M5 12h14M13.5 6.5 19 12l-5.5 5.5"/>`},
	"chevron":        {"1.8", true, `<path d="m9.5 6.5 5.5 5.5-5.5 5.5"/>`},
	"admin":          {"1.6", true, `<path d="M12 3 5 6v5c0 4.5 3 8 7 10 4-2 7-5.5 7-10V6z"/><rect x="9.5" y="10.5" width="5" height="4" rx=".8"/><path d="M10.5 10.5v-1a1.5 1.5 0 0 1 3 0v1"/>`},
}

// icon renders a named icon as decorative inline SVG (aria-hidden: the
// surrounding control or text always carries the accessible name). An
// unknown name is a template execution error, so typos fail tests.
func icon(name string) (template.HTML, error) {
	shape, ok := icons[name]
	if !ok {
		return "", fmt.Errorf("unknown icon %q", name)
	}
	caps := ""
	if shape.round {
		caps = ` stroke-linecap="round" stroke-linejoin="round"`
	}
	return template.HTML(`<svg class="icon icon-` + name + `" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="` +
		shape.strokeWidth + `"` + caps + ` aria-hidden="true" focusable="false">` + shape.body + `</svg>`), nil
}
