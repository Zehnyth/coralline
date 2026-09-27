# Browse UI

Server-rendered Go templates plus a handful of native ES-module components.
There is no SPA, no npm, and no frontend build step. Everything is embedded in
the Go binary.

- The home page is a text hero with a link to the getting-started guide.
- Namespace and module lists support search, sorting, and (for modules)
  provider filtering. The view is kept in the URL, so it can be bookmarked.
  Listings only include namespaces the signed-in token can read.
- Module pages support version selection and copying the registry source and
  pinned configuration. The newest stable release is the default
  ("Recommended"); a module with only pre-releases shows "Latest" instead.
  Admins also see which token published the selected version.
- The Tokens section (admins only) lists every token with its grants, tags, and
  last use; filters by tag; creates standard tokens; and edits grants and tags
  or revokes existing ones. Admin tokens are read-only here.
- A new token's secret is shown **once**, on `/tokens/{id}/created`, and only to
  the browser session that created it. Creating the token redirects there; the
  secret is never in a URL. Every create form carries a single-use submission
  key, so a double click, a refresh, or Back + resubmit can't mint twice.
- The sign-in page (`/login`) is standalone: it renders without the app shell.
- Unknown pages, and pages the viewer may not know exist, get a styled 404
  inside the shell. Every HTML page is `Cache-Control: no-store`.
- The theme follows the OS until the viewer picks one with the toggle.
- Layouts adapt at 1180, 960, and 700 pixels (the sidebar becomes a drawer at
  700), with keyboard focus rings, a skip link, labelled controls, one `<h1>`
  per page, and reduced-motion support.

## How it's put together

The UI never authenticates anyone. `app/httpserver` owns sessions, CSRF, the
sign-in redirect, the admin gate, and the session-bound store behind
`RevealStore`. It hands the UI a `Viewer` resolver, a `PublisherRepo`, a
`TokenAdmin`, and a `RevealStore`. Handlers still re-check visibility, answer
404 rather than 403 for anything the viewer can't see, and fail closed when no
`Viewer` is configured.

```
*.go                     handlers.go (wiring), render.go (templates, error pages),
                         assets.go (static files), funcs.go + icons.go (template
                         funcs), viewmodels.go / tokens_view.go (typed page data),
                         browse.go, module.go, tokens.go, reveal.go, login.go, semver.go
templates/
  layout.html            app shell; pages fill "title", "styles", "content"
  shell/                 pieces only the shell uses: head, sidebar, topbar,
                         session-controls, guide-dialog, toast
  components/            reusable components (catalog, module-card, empty-state, …)
  pages/                 one template per page (login.html is standalone)
static/
  css/base.css           design tokens (light-dark()), reset, type, utilities
  css/layout.css         the shell
  css/components/*.css   one file per component
  css/pages/<page>.css   rules used by one page only
  js/app.js              entry point: mounts every data-component
  js/core/               component.js (runtime), dom.js, hooks.js, toast.js
  js/components/*.js     one module per component
  img/favicon.svg
```

### Components

A component is a name shared by up to three files:

| File | Role |
|---|---|
| `templates/components/<name>.html` | `{{define "<name>"}}` (helpers may be `<name>-*`), with a comment listing its props |
| `static/css/components/<name>.css` | its styles, including its responsive rules |
| `static/js/components/<name>.js` | its behaviour, if it has any |

Props go in as a typed struct, or with `dict` for small ones:
`{{template "empty-state" dict "Icon" "cube" "Title" "Nothing yet."}}`. Icons
are `{{icon "cube"}}` (see `icons.go`). Only print a `dict` key that's always
passed; use `if`/`with` for optional ones.

Behaviour attaches through `data-component="<name>"`. `static/js/app.js` mounts
every such element, importing `js/components/<name>.js` on first use, and
calls its default export once per element:

```js
import { combine, on } from "../core/dom.js";
import { useDebounce } from "../core/hooks.js";

export default function example(root, { part, parts, props }) {
  const input = part("input");           // data-part="input", inside root only
  const save = useDebounce(() => {}, 300);
  return combine(on(input, "input", save), save.cancel); // cleanup
}
```

- A component only touches its own element and its `data-part`s. Components
  talk to each other through `emit`/`listen` events (e.g. `guide:open`,
  `sidebar:toggle`), never by querying each other's markup.
- `core/hooks.js` holds reusable behaviour as plain functions (`useUrlState`,
  `useDebounce`, `useStorage`, `useMediaQuery`). They're in the spirit of React
  hooks, with no framework or re-rendering. Anything that subscribes returns a
  way to unsubscribe.
- An element may carry several components: `data-component="confirm-form submit-once"`.

Tests enforce the contract: template `define` names and file names must be
unique (`checkUniqueDefines`), and every `data-component` needs a script, just
as every script needs a user (`structure_test.go`).

### Assets and caching

Templates link files with `{{asset "css/pages/home.css"}}`. In production that
renders `/static/<fingerprint>/css/pages/home.css`, where the fingerprint is a
hash of the whole static tree. Fingerprinted URLs are served
`immutable`, so each file is downloaded once per release. ES modules import
siblings by relative path and inherit the fingerprint. `css/app.css` is a
virtual file: `base.css`, `layout.css`, then `components/*.css` in name order,
concatenated at startup. Unversioned `/static/...` paths still work and
revalidate with an ETag.

### Adding a page or component

1. Page: add `templates/pages/<page>.html` (defining "title", "content", and
   "styles" if it needs `css/pages/<page>.css`), a typed page struct embedding
   `basePage`, and a handler that calls `h.render`.
2. Component: add `templates/components/<name>.html` and, as needed,
   `static/css/components/<name>.css` and `static/js/components/<name>.js`.
   Promote a rule or markup into a component only when a second page needs it.

## Development

Run `go test ./ui` for handler and structure checks. For a browser preview with
synthetic data only, from the repository root:

```powershell
$env:CORALLINE_UI_PREVIEW = "1"
$env:CORALLINE_UI_PREVIEW_ROLE = "admin"   # or "standard" / "anonymous"
go test ./ui -run TestBrowsePreview -timeout 0 -v
```

Open `http://127.0.0.1:8787`. Set `CORALLINE_UI_PREVIEW_ADDR=0.0.0.0:8787` to
run it in a container. The preview never opens the registry database and
performs no real authentication: the role variable picks the viewer, and the
Tokens section runs against in-memory fixtures. CSS and JS are served from disk
uncached (reload to see changes); templates are parsed at startup (restart).
The real application embeds every asset, so rebuild and restart it to apply UI
updates.

Known follow-up: catalogs render every module and filter in the browser. That
works for hundreds of modules. Server-side pagination needs a decision on page
size and URL shape.
