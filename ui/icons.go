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
	"grid":     {"1.6", false, `<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`},
	"cube":     {"1.5", false, `<path d="m12 2 9 5v10l-9 5-9-5V7zM3 7l9 5 9-5M12 12v10M7.5 4.5l9 5"/>`},
	"search":   {"1.6", false, `<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/>`},
	"copy":     {"1.6", false, `<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M15 8V4H4v11h4"/>`},
	"sun":      {"1.6", false, `<circle cx="12" cy="12" r="4.5"/><path d="M12 2.5v3M12 18.5v3M4.4 4.4l2.1 2.1M17.5 17.5l2.1 2.1M2.5 12h3M18.5 12h3M4.4 19.6l2.1-2.1M17.5 6.5l2.1-2.1"/>`},
	"moon":     {"1.6", false, `<path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a7 7 0 0 0 10.5 10.5z"/>`},
	"collapse": {"1.8", true, `<path d="M14.5 6.5 9 12l5.5 5.5"/>`},
	"menu":     {"1.6", false, `<path d="M3.5 6.5h17M3.5 12h17M3.5 17.5h17"/>`},
	"close":    {"1.8", true, `<path d="M6 6l12 12M18 6 6 18"/>`},
	"home":     {"1.6", true, `<path d="M4 11.5 12 4l8 7.5"/><path d="M6 10.5V19a1 1 0 0 0 1 1h3v-5.5h4V20h3a1 1 0 0 0 1-1v-8.5"/>`},
	"key":      {"1.6", true, `<circle cx="7.5" cy="14.5" r="3.3"/><path d="M9.8 12.2 18.7 3.3"/><path d="M15.2 6.8l2.3 2.3"/><path d="M17.7 4.3l2.3 2.3"/>`},
	"plus":     {"1.8", true, `<path d="M12 5v14M5 12h14"/>`},
	"lock":     {"1.6", true, `<rect x="5" y="10.5" width="14" height="10" rx="2"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>`},
	"layers":   {"1.6", true, `<path d="m12 3 9 5-9 5-9-5z"/><path d="m3 13 9 5 9-5"/>`},
	"shield":   {"1.6", true, `<path d="M12 3 5 6v5c0 4.5 3 8 7 10 4-2 7-5.5 7-10V6z"/><path d="m9 12 2 2 4-4"/>`},
	"alert":    {"1.6", true, `<circle cx="12" cy="12" r="9"/><path d="M12 7.5v5.5M12 16.5v.01"/>`},
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
