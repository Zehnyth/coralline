# Architecture

Coralline is a self-hosted Terraform/OpenTofu module registry: it implements the
[registry protocol](https://developer.hashicorp.com/terraform/internals/module-registry-protocol)
(discovery, versions, download) plus a two-phase publish protocol, backed by
SQLite (index) and a filesystem blob store. This document describes how the
pieces fit together; see [CLAUDE.md](CLAUDE.md) for day-to-day commands and
conventions.

## Processes and listeners

A single binary (`cmd/coralline`) starts two independent HTTP listeners from
[cmd/coralline/serve.go](cmd/coralline/serve.go):

```
                        ┌──────────────────────────────────┐
 terraform init  ─────▶ │ Public listener  (TLS, :8443)      │
 browser         ─────▶ │  - /.well-known/terraform.json     │
                        │  - registry protocol routes         │
                        │  - /login, /logout (sessions)       │
                        │  - browse UI (templates + ES mods)  │
                        │  - Tokens section (admin sessions)  │
                        └────────────────┬──────────────────┘
                                         │
                                         ▼
                        ┌──────────────────────────────────┐
                        │ app/httpserver mux + session gate  │
                        └────────────────┬──────────────────┘
                                         │
                   ┌─────────────────────┼────────────────────┐
                   ▼                     ▼                    ▼
             app/auth             app/modules /          app/storage
          (Verifier, MintToken)    sqlitestore             (FSStore)
                                  (reads/writes)

                        ┌──────────────────────────────────┐
 coralline CLI   ─────▶ │ Admin listener (loopback-only,     │
 (namespace/token/      │  :8444, separate net.Listener)     │
  admin)                │  - namespace / token JSON API       │
                        │  - the only admin-token minting     │
                        └──────────────────────────────────┘
```

- **Public listener** (`httpserver.New`, TLS by default): discovery
  (`/.well-known/terraform.json`) is unauthenticated; module read/download/
  publish routes go through `auth.Verifier.RequireScope`. Publishing always
  requires a token. With `auth.require_login` on (the default), browser pages
  redirect to `/login` and registry reads need a bearer token; turning it off
  makes browsing and downloads anonymous. The browser session gate
  (`app/httpserver/session.go`) handles sign-in, CSRF, origin checks, and the
  admin-only gate on `/tokens*`.
- **Admin listener** (`httpserver.NewAdminServer`, loopback-only): the
  namespace and token JSON API, authenticated with an admin token through the
  same `Verifier` as everything else. It is deliberately never mounted on the
  public mux: namespace creation and admin-token minting are not reachable from
  the public surface. The CLI's admin subcommands call this HTTP API rather than
  opening the SQLite file directly, since SQLite allows one writer and a second
  process opening the same file races with the running server (see
  [cmd/coralline/adminclient.go](cmd/coralline/adminclient.go)).

Both listeners share the same `app/modules`, `app/sqlitestore`, `app/storage`,
and `auth.Verifier` instances in-process; there is no RPC between them.

## Package layout

```
cmd/coralline/        CLI entrypoint: serve, namespace, token, admin subcommands
app/
  domain/             Namespace, Module, Version, Token, Permission, Grant, Tag + sentinel errors
  config/             YAML config loading (every field optional)
  storage/             BlobStore interface + FSStore (filesystem) impl
  sqlitestore/         repository layer (DB), embedded migrations, WAL mode
  auth/                token parsing/hashing, MintToken, admin bootstrap, rate limiting, Verifier
  modules/             Publisher (two-phase publish) + reservation reaper
  transport/terraform/ registry protocol handlers (discovery/versions/download/publish)
  transport/admin/     admin JSON API handlers
  httpserver/          mux wiring, session gate, ui.TokenAdmin adapter, /healthz, /readyz, TLS listener
  logging/             structured slog JSON logging, correlation IDs, audit events
  tlsutil/             self-signed cert generation for local evaluation
ui/                    server-rendered browse UI (Go templates + native ES-module components)
```

Dependency direction is one-way and enforced by what each package is allowed
to import:

```
transport/terraform, transport/admin, httpserver, ui
        │
        ▼
  modules, auth  ───▶  sqlitestore, storage
        │                     │
        ▼                     ▼
              domain (types + sentinel errors)
```

`ui/` is split out as its own top-level package (`coralline/ui`, not
`app/ui`) because it depends only on `app/domain` and `app/logging` — never
directly on storage, auth, or SQLite. `app/httpserver` is the only package
that wires it in, injecting three narrow collaborators: a `Viewer` resolver
(who is asking, and which namespaces they can read), a `PublisherRepo`, and a
`TokenAdmin`. The UI authenticates no one; its handlers re-check visibility
and fail closed when no `Viewer` is configured.

`app/modules` is the only package allowed to *write* through the SQLite
repository layer for version state — a CQRS-lite split where pure reads
(discovery, listing versions, download URL lookup) go directly through
`sqlitestore` with no indirection, but anything that mutates a version's
lifecycle goes through `modules.Publisher`. Tokens are created only through
`auth.MintToken`, which the admin JSON API, the web UI, and first-boot
bootstrap all share, so token validation can't drift between surfaces.

## Data model

Two persistence stores, kept in sync by the publish protocol:

- **SQLite** (`app/sqlitestore`, WAL mode, foreign keys on) — the index:
  `namespaces`, `tokens` (with `kind`, `name`, `created_by`, `last_used_at`),
  `token_grants`, `token_tags`, `versions`, `downloads`. Schema lives in
  append-only `app/sqlitestore/migrations/*.sql`, embedded into the binary and
  applied via `PRAGMA user_version`.
- **Filesystem blob store** (`app/storage.FSStore`) — the actual module
  archive bytes, keyed by namespace/name/system/version, with a separate
  `_staging/...` prefix used mid-publish.

`versions.status` is the state machine driving what's visible on reads:

```
reserve ──▶ uploading ──▶ (stage + promote succeed) ──▶ active   (visible)
                    └────▶ (any failure)             ──▶ failed  (hidden, reaped)
```

Only rows with `status = active` are ever returned by discovery/versions/
download handlers. `downloads` and `tokens.last_used_at` are written from
buffered channels and flushed in batches — never a synchronous write per
request — to keep the hot download and authentication paths off the SQLite
write lock.

### Domain errors

`app/domain/errors.go` defines sentinel errors (`ErrNotFound`, `ErrConflict`,
`ErrUnauthorized`, `ErrForbidden`, `ErrInvalid`, `ErrRateLimited`) that every
lower layer returns (wrapped with `%w`) and every transport adapter maps to
its own wire format via `errors.Is`/`errors.As`. No layer inspects error
strings for control flow — this is what lets `transport/terraform`,
`transport/admin`, and the UI share the same domain/service layer while
producing different response shapes.

## Two-phase publish protocol

Implemented in `modules.Publisher.Publish`, invoked from
[app/transport/terraform/publish.go](app/transport/terraform/publish.go):

1. **Reserve** — insert the version row in SQLite first, status
   `uploading`. The `(namespace, name, system, version)` primary key gives
   atomic conflict detection before any blob bytes are touched, closing a
   race where two concurrent publishes of the same version could otherwise
   leave the index pointing at a blob the losing request deletes.
2. **Stage** — stream the upload to a `_staging/...` key in the blob store,
   computing SHA-256 in the same pass via `io.TeeReader`. The final key is
   never written to directly.
3. **Promote** — atomically move staging → final key. On the filesystem
   backend this is `Get` + `Put` + `Delete` (write-then-rename under the
   hood); the staging file handle is explicitly closed before delete, since
   Unix permits unlinking an open file but Windows does not. A future S3
   backend would instead use a conditional `PutObject`/`CopyObject` with
   `If-None-Match: *`.
4. **Activate** — flip the row to `active`. Only now does it become visible
   to discovery/version-list/download reads.

Any failure after the reserve step marks the row `failed` rather than
deleting it (so a client retry sees a clear conflict, not a phantom missing
version). A background reaper (`modules.RunReaper`, started from
`serve.go`) periodically cleans up timed-out `uploading` reservations and
orphaned staging blobs, so a crashed publish doesn't wedge that version
number forever.

## Auth model

Tokens are the only credential. There are no user accounts, passwords, or
external identity providers — deliberately, because zero-config setup is the
product. Each person or pipeline gets its own token, and the token is the
principal.

### Verifying a token

Bearer tokens have the shape `crl_pat_<id>_<secret><crc32>` (`app/auth`):

1. CRC32 is checked first, before any DB lookup or hashing — cheap rejection
   of malformed/corrupted tokens.
2. Per-IP rate limiting (`app/auth/ratelimit.go`).
3. The `id` half is looked up directly (indexed, cheap). Unknown ids are
   rejected without running a dummy argon2 hash — the timing side-channel
   this creates is accepted deliberately, given 128-bit id entropy, to avoid
   handing attackers a free CPU-exhaustion vector via a dummy-hash path.
4. The `secret` half is verified against the stored argon2id hash, gated
   behind a bounded concurrency semaphore (`auth.ArgonSemaphore`).
5. Revoked and expired tokens are rejected.

`Verifier.Authenticate` reads the `Authorization` header; the web sign-in form
calls `Verifier.AuthenticateToken` with a pasted token, so both go through
exactly the same steps. The admin JSON API uses the same `Verifier` too.

### Kinds, grants, and tags

- **Kinds.** `tokens.kind` is `admin` or `standard`. Admin capability is a
  kind, never a grant string, so no permission a standard token can hold —
  wildcards included — can confer token or namespace management.
- **Grants.** Standard tokens hold IAM-style grants, `Grant{Namespace,
  Permission}`: `module:read`, `module:publish`, or `module:*`, on one
  namespace or `*`. `Token.Allows(namespace, permission)` is the single
  authorization decision. The grantable set is closed and unknown strings are
  rejected at mint time; granted namespaces must exist. Roles (`reader`,
  `publisher`) are CLI/form sugar, expanded and stored as explicit grants.
- **Metadata.** Every token has a display name. `created_by` (the minting admin
  token's id) and `last_used_at` are server-set. Tags are free-form,
  admin-asserted labels (`owner=alice`, `email=…`, `team=…`) used for
  attribution and offboarding (`coralline token revoke --tag owner=alice
  --all`). Tags never participate in authorization, are never loaded on the
  authentication path, and are only ever rendered to admins.

### Who can do what

- Only admin tokens can see or manage tokens. Standard and anonymous visitors
  get 404 on every `/tokens*` route, don't see the Tokens navigation, and
  never see token names or publisher attribution.
- Admin tokens are minted only on the loopback admin API (`coralline admin
  create`, `coralline admin rotate`). The web UI mints standard tokens only and
  treats admin tokens as read-only, so a hijacked browser session can't grant
  itself lasting admin access.
- The server mints and prints a `bootstrap-admin` token whenever it starts with
  no active admin token — first boot, and lockout recovery by restart.
  Revoking the last active admin token is refused.

### Browser sessions

Signing in at `/login` verifies the pasted token and starts an in-memory
session: a random id in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie, with
a 1-hour idle and 12-hour absolute lifetime. Sessions end on restart by
design. Every request re-checks the session's token (indexed lookup, no
argon2id), so revoking or expiring a token ends its sessions immediately.
State-changing requests require a per-session CSRF value and a same-origin
`Origin`/`Referer`; the sign-in form itself requires a same-origin `Origin`.
The `next` redirect only accepts same-origin paths. Every response carries
`X-Frame-Options: DENY`, `frame-ancestors 'none'`, `nosniff`, and
`Referrer-Policy: same-origin`.

## Request flows

**`terraform init` / module resolution:**

```
terraform init
   │
   ▼
GET /.well-known/terraform.json          (public, unauthenticated)
   │
   ▼
GET /v1/modules/{ns}/{name}/{system}/versions   (auth.RequireScope: module:read)
   │
   ▼
GET /v1/modules/{ns}/{name}/{system}/{version}/download   (auth.RequireScope: module:read)
   │  → 204 with X-Terraform-Get pointing at the archive endpoint
   ▼
GET .../{version}/archive?archive=tar.gz   (auth.RequireScope: module:read)
```

**Publishing (via a token holding `module:publish`):**

```
PUT .../{version}/publish  (auth.RequireScope: module:publish)
   │
   ▼
modules.Publisher.Publish
   reserve (SQLite) → stage (blob, staging key) → promote (blob, final key) → activate (SQLite)
```

**Browse UI:** server-rendered Go templates with small native ES-module components (no build step), served
from `app/httpserver` under the public listener. Templates split into
`ui/templates/layout.html`, `shell/`, reusable `components/`, and one file per
page under `pages/`; each component pairs its template with
`static/css/components/<name>.css` and, when it has behaviour,
`static/js/components/<name>.js` (see `ui/README.md`). Assets are served
under content-fingerprinted, immutable URLs. Listings are scoped to the
namespaces the viewer can read. The Tokens section is the UI's only write
path, and it goes through `ui.TokenAdmin` → `auth.MintToken`/`sqlitestore`,
with every change audit-logged against the acting admin.

## Configuration and zero-config defaults

`app/config` loads YAML where every field is optional; `Load("")` returns a
fully working default configuration (listen on `:8443` with a self-signed
cert, blobs under `./data/blobs`, SQLite at `./data/index.db`, admin API on
`127.0.0.1:8444`, `require_login: true`). This zero-config behavior is a
deliberate design principle carried through every package, not just the
top-level `serve` command — see [config/coralline.yml](config/coralline.yml)
for the overridable fields.

TLS is non-optional on the public listener in production because Terraform's
discovery protocol requires HTTPS unconditionally and `terraform init` cannot
be made to bypass an untrusted cert; `app/tlsutil` generates a self-signed
cert for local evaluation only — a real deployment needs a trusted cert or a
terminating reverse proxy in front of it. Session cookies are always
`Secure`, which works behind a TLS-terminating proxy and on `localhost`.

## Observability

`app/logging` provides structured `slog` JSON logging with per-request
correlation IDs, threaded through as HTTP middleware. Audit events (namespace
and token creation, grant and tag changes, revocations, sign-ins and failed
sign-ins) are always logged and name the acting token. `/healthz` is a
liveness check with no dependency checks (safe for a container orchestrator's
fast probe); `/readyz` additionally checks that SQLite is pingable.
