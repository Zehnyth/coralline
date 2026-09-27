# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Coralline is a self-hosted Terraform/OpenTofu module registry: it implements the registry protocol (discovery, versions, download) and a two-phase publish protocol, backed by SQLite (index) + a filesystem blob store. Go 1.22, module path `coralline`.

Backend code lives under [app/](app/) (e.g. `coralline/app/auth`, `coralline/app/httpserver`); the server-rendered browse UI is its own top-level package, [ui/](ui/) (`coralline/ui`) — split out because it only depends on shared domain types and logging, never on storage/auth/SQLite directly.

## Commands

```bash
go mod tidy              # sync go.mod/go.sum (needs real network — see below)
go build ./...            # compile everything
go vet ./...
go test ./...              # full suite
go test ./app/modules -run TestPromoteClosesStagingFileBeforeDelete -v   # single test
gofmt -w <files>           # format changed files before committing
```

Run `go test ./...`, `go vet ./...`, and `go build ./...` before opening a PR.

**Dependency fetch note:** this sandbox's network doesn't reach the real Go module proxy, so `go mod tidy`/`go build` may need to run on a machine with normal internet access first (see [README.md](README.md)).

Run the server:

```bash
go run ./cmd/coralline serve
```

Zero-config defaults: listens on `:8443` (self-signed TLS), filesystem blobs under `./data/blobs`, SQLite index at `./data/index.db`, admin API on loopback `127.0.0.1:8444`, `auth.require_login: true`. An admin token is minted and printed whenever the server starts with no active admin token (first boot, or after every admin token was revoked). `serve` loads [config/coralline.yml](config/coralline.yml) by default (`--config` to point elsewhere); it lists every field at its default, and a missing file falls back to the same defaults.

```bash
export CORALLINE_ADMIN_CREDENTIAL="<printed admin token>"
go run ./cmd/coralline namespace create acme
go run ./cmd/coralline token create --name ci --namespace acme --role publisher --tag owner=platform
```

Browse-UI-only preview with synthetic data (no DB, in-memory token fixtures, no real auth):

```powershell
$env:CORALLINE_UI_PREVIEW = "1"
$env:CORALLINE_UI_PREVIEW_ROLE = "admin"   # or "standard" / "anonymous"
go test ./ui -run TestBrowsePreview -timeout 0 -v
```
Then open `http://127.0.0.1:8787` (`/login` shows the sign-in page). Template changes require restarting it; CSS/JS are served from disk. Local binaries can be blocked on this machine; to run the preview in a container, add `CORALLINE_UI_PREVIEW_ADDR=0.0.0.0:8787` and publish port 8787.

`data/` is local runtime state (SQLite files, blobs, dev certs) — never commit anything generated under it.

## Architecture

Two listeners, started from [cmd/coralline/serve.go](cmd/coralline/serve.go):
- **Public listener** (`httpserver.New`, TLS by default): Terraform registry protocol, browse UI, sign-in, and the admin-only Tokens section. Discovery (`/.well-known/terraform.json`) is unauthenticated; module read/download/publish routes go through `auth.Verifier.RequireScope`; publish always requires a token. With `require_login` on (the default), browser pages redirect to `/login` and registry reads need a bearer token. Browser auth (sessions, CSRF, origin checks, the admin gate) lives in [app/httpserver/session.go](app/httpserver/session.go).
- **Admin listener** (`httpserver.NewAdminServer`, loopback-only, separate `net.Listener`): namespace/token JSON API authenticated with an admin token through the same `Verifier`. It is the only place admin tokens can be minted and namespaces created — never mounted on the public mux. The CLI's admin subcommands call this HTTP API rather than opening the SQLite file directly, because SQLite allows one writer and a second process opening the same file races with the running server (see [cmd/coralline/adminclient.go](cmd/coralline/adminclient.go)).

Package layout under `app/`:
- `domain/` — shared types (`Namespace`, `Module`, `Version`, `Token`, `TokenKind`, `Permission`, `Grant`, `Tag`) and sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrUnauthorized`, `ErrForbidden`, `ErrInvalid`, `ErrRateLimited`). `Token.Allows` is the single authorization decision. Every transport adapter maps errors to its own wire format via `errors.Is`/`errors.As` — no layer inspects error strings for control flow.
- `config/` — YAML config loading; every field optional, `Load("")` returns working defaults (zero-config is a deliberate design principle throughout).
- `storage/` — `BlobStore` interface + `FSStore` (filesystem) implementation; atomic write-then-rename, path-traversal guards. S3/MinIO is scoped out of this pass but the interface is ready for it.
- `sqlitestore/` — repository layer (`DB`), embedded append-only schema migrations (`migrations/*.sql`), WAL mode, foreign keys on. Owns namespaces, tokens (+ `token_grants`, `token_tags`), versions, buffered download attribution, and buffered token last-used tracking.
- `auth/` — token format (`crl_pat_<id>_<secret><crc32>`, CRC checked before any DB/hash work), argon2id hashing behind a concurrency semaphore (`ratelimit.go`), per-IP rate limiting, `MintToken` (the one token-creation path), `EnsureAdminToken` bootstrap, `Verifier.Authenticate`/`AuthenticateToken`/`RequireScope`.
- `modules/` — `Publisher`, the CQRS-lite "write" coordinator spanning SQLite + blob storage, plus the reservation reaper (`reaper.go`). Pure reads live directly in `sqlitestore` with no indirection through here.
- `tlsutil/` — self-signed cert generation for local evaluation (production needs a trusted cert or a terminating reverse proxy — Terraform's discovery protocol requires HTTPS unconditionally and `terraform init` cannot bypass an untrusted cert).
- `transport/terraform/` — the registry protocol handlers (discovery, versions, download, publish), verified against HashiCorp's reference response shapes.
- `transport/admin/` — the admin JSON API handlers.
- `httpserver/` — mux wiring, the browser session gate (`session.go`), the `ui.TokenAdmin` adapter (`tokenadmin.go`), security headers, `/healthz` (liveness, no dependency checks) and `/readyz` (checks SQLite is pingable), TLS listener wrapping.
- `logging/` — structured `slog` JSON logging, correlation IDs, HTTP middleware, audit events.

[ui/](ui/) — server-rendered browse UI (Go templates + native ES-module components, no build step, embedded via `embed.FS`), package `coralline/ui`; see [ui/README.md](ui/README.md) for the component contract. Depends only on `app/domain` and `app/logging`, and is only imported by `app/httpserver`. It never authenticates anyone: `httpserver` injects a `ui.Viewer` resolver, a `PublisherRepo`, a `TokenAdmin`, and a `RevealStore` (session-bound, in-memory, one-time display of newly minted token secrets — `app/httpserver/reveal.go`); handlers re-check visibility and fail closed without a Viewer. Static files are served by `ui.Assets` under fingerprinted, immutable URLs; every HTML page is `no-store`.

### Two-phase publish protocol (`modules.Publisher.Publish`)

1. **Reserve** the version row in SQLite first — the UNIQUE constraint gives atomic conflict detection before any blob is touched (closes a race where two concurrent publishes of the same version could leave the index pointing at a blob the loser deleted).
2. **Stage**: stream the upload to a `_staging/...` key, computing SHA-256 in the same pass via `io.TeeReader`, never writing directly to the final path.
3. **Promote**: atomic move staging → final key. On the filesystem backend this is `Get`+`Put`+`Delete` (write-then-rename under the hood); the staging file handle is explicitly closed before delete — Unix permits unlinking an open file, Windows does not, so ordering here matters for Windows correctness. An S3 backend (future) would instead use a conditional `PutObject`/`CopyObject` with `If-None-Match: *`.
4. **Activate** the row — only `active` versions are ever visible on reads.

Any failure after reservation marks the row `failed`; a background reaper (`modules.RunReaper`, started from `serve.go`) periodically cleans up timed-out reservations and orphaned staging blobs.

### Auth model

Tokens are the only credential — there are no user accounts, passwords, or external identity providers, by design. Each person or pipeline gets its own token, and the token is the principal.

- Bearer tokens (`crl_pat_...`) carry an id (cheap, indexed lookup) and a secret (argon2id-verified). Unknown token ids are rejected without running a dummy hash — the timing side-channel is accepted deliberately given 128-bit id entropy, to avoid handing attackers a free CPU-exhaustion vector. Argon2id verification is gated behind `auth.ArgonSemaphore` (bounded concurrency).
- `tokens.kind` is `admin` or `standard`. Admin capability is a kind, never a grant string, so no standard-token permission (wildcards included) can confer token or namespace management. Only admin tokens can see or manage tokens; standard sessions get 404 on every `/tokens*` route.
- Standard tokens hold IAM-style grants: `Grant{Namespace, Permission}` with `module:read`, `module:publish`, or `module:*`, on one namespace or `*`. The grantable set is closed — unknown strings are rejected at mint time. Roles (`reader`, `publisher`) are CLI/form sugar expanded at mint time.
- Tokens carry a name, server-set `created_by`/`last_used_at`, and free-form admin-asserted tags (`owner`, `email`, …). Tags never participate in authorization, are never loaded on the auth path, and are only rendered to admins.
- Admin tokens are minted only on the loopback API (`coralline admin create/rotate`); the web UI mints standard tokens only and treats admin tokens as read-only. Revoking the last active admin token is refused.
- Browser sessions are in-memory, created by pasting a token at `/login`; every request re-checks the token, so revocation ends sessions immediately.

## Conventions

- Keep storage/repository details behind interfaces (`storage.BlobStore`, `modules.VersionRepo`, `auth.TokenLookup`, `auth.TokenStore`, `ui.TokenAdmin`) — packages narrow their dependencies to only what they need, partly to avoid import cycles.
- Create tokens only through `auth.MintToken`; take the acting admin from the authenticated token/session, never from request input.
- Reuse `domain` sentinel errors and wrap with `%w`; don't inspect error strings across layers.
- UI structure: `ui/templates/layout.html` is the shell; `templates/shell/` holds pieces only the shell uses; `templates/components/` holds reusable components; one file per page in `templates/pages/`. A component `<name>` is `templates/components/<name>.html` + `static/css/components/<name>.css` + (if it has behaviour) `static/js/components/<name>.js`, attached with `data-component="<name>"`; its JS only touches its own element and `data-part`s, and components talk through `emit`/`listen` events. Reusable JS behaviour goes in `static/js/core/hooks.js` as `use*` functions. Page-only CSS goes in `static/css/pages/<page>.css`, linked with `{{asset "…"}}` from the page's `{{define "styles"}}` block. Colours come from the semantic tokens in `static/css/base.css`, never raw hex in components. Page data is a typed struct embedding `basePage`, never `map[string]any`. Template `define` names and file basenames must be unique, and every `data-component` needs a script; `ui/structure_test.go` enforces both. Don't grow a single shared stylesheet or script.
- Tests live beside implementation as `*_test.go` (`TestName`, table-driven where useful); use `t.TempDir()` and isolated SQLite DBs, never mutate checked-in `data/`. Use keyed fields for composite literals of imported struct types (`go vet`).
- Commit subjects: short, imperative, optionally scoped (e.g. `auth: reject expired publish tokens`).
