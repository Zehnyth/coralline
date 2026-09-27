# Coralline — first prototype

This is a first-pass implementation of Coralline's core: the Terraform/OpenTofu
module registry protocol and the two-phase publish protocol, per the v1 PRD.

## Required first step

This project was written and compile-verified in a sandboxed environment whose
network is restricted to a small allow-list that does **not** include the real
Go module proxy. Real compilation was verified using local stub packages that
match the exact public API of each dependency (`modernc.org/sqlite`,
`golang.org/x/crypto/argon2`, `gopkg.in/yaml.v3`), then swapped back out — the
`go.mod` you have points at genuine upstream modules, not the stubs.

**On your own machine, with normal internet access, run this once:**

```bash
go mod tidy
go build ./...
```

This downloads the real dependencies and generates `go.sum`. From then on it
builds like any other Go project.

## Running it

```bash
go run ./cmd/coralline serve
```

With no config file, this starts a working registry on `:8443` with a
self-signed certificate, a filesystem blob store under `./data/blobs`, and a
SQLite index at `./data/index.db`. On first boot the server prints an **admin
token** once — save it. A new one is printed whenever the server starts with no
active admin token, so an operator who loses every admin token recovers by
restarting.

Open `https://localhost:8443`, sign in with the admin token, and use the
**Tokens** section to mint tokens for people and pipelines. Or use the CLI:

```bash
export CORALLINE_ADMIN_CREDENTIAL="<the admin token printed above>"
go run ./cmd/coralline namespace create acme
go run ./cmd/coralline token create --name ci-publisher --namespace acme --role publisher --tag owner=platform
```

Use the printed token as `Authorization: Bearer <token>` to publish:

```bash
curl -X PUT --data-binary @mymodule.tar.gz \
  -H "Authorization: Bearer <token>" \
  https://localhost:8443/v1/modules/acme/vpc/aws/1.0.0/publish
```

**Important — read before deploying anywhere beyond your own machine:**
Terraform's remote service discovery protocol requires HTTPS unconditionally,
and `terraform init` has no way to bypass an untrusted certificate. The
auto-generated certificate above is for local evaluation only. Real use from a
second machine needs either a certificate your client already trusts, or a
reverse proxy (Caddy, Traefik, nginx) terminating TLS with one in front of
Coralline.

### Tokens and access

Tokens are the only credential — there are no user accounts or passwords.
Give each person or pipeline their own token:

- **Permissions** are granted per namespace (or `*` for all): `module:read`,
  `module:publish`, or `module:*`. `--role reader` and `--role publisher` are
  shortcuts.
- **Tags** label who a token belongs to (`--tag owner=alice --tag team=networking`).
  They never grant access, and only admins can see them. Offboarding is one
  command: `coralline token revoke --tag owner=alice --all`.
- **Admin tokens** can see and manage every token; nobody else can see tokens
  at all. Admin tokens are created and rotated only from the CLI on the
  registry host (`coralline admin create`, `coralline admin rotate`).
- **`auth.require_login`** (default `true`) puts the browse UI behind sign-in and
  registry reads behind a bearer token. Set it to `false` for anonymous browsing
  and downloads; publishing always needs a token.

## What's implemented

- Terraform/OpenTofu registry protocol: discovery, versions, download,
  verified against HashiCorp's published reference (exact response shapes,
  `Content-Type` on the empty `204`, `X-Terraform-Get` format)
- Two-phase publish protocol: reserve → stage → promote → activate, with a
  background reaper for timed-out reservations
- SQLite repository layer, WAL mode, embedded append-only schema migrations
- Filesystem `BlobStore`, with atomic write-then-rename and path-traversal
  guards
- Auth: CRC-checked token format, argon2id hashing, per-source rate limiting, a
  global argon2id concurrency semaphore, admin and standard token kinds,
  per-namespace `module:*` permissions, token names and tags, last-used tracking,
  admin token bootstrap and rotation
- Browser sign-in with token-backed sessions, CSRF protection, and an
  admin-only Tokens section (list, filter by tag, create, edit grants and tags,
  revoke)
- Buffered download attribution (never a synchronous insert per download)
- Structured JSON logging with correlation IDs and audit events naming the
  acting token
- `/healthz` and `/readyz`
- Self-signed TLS generated on first boot
- A server-rendered browse UI (Go templates + small ES-module components), scoped to the
  namespaces the signed-in token can read
- CLI: `serve`, `namespace create/list`, `token create/list/revoke/tag/untag`,
  `admin create/rotate` — admin commands call the admin API over loopback rather
  than opening the SQLite file directly, avoiding two processes contending for
  the same database file

## What's deliberately not in this pass

Scoped out to keep this prototype reviewable, not because any of it is hard:

- **S3/MinIO backend.** The `BlobStore` interface is ready for it; only the
  filesystem implementation exists so far.
- **Backup, export, import.** Replaces an earlier "rebuild the index from
  storage" design that turned out to be unable to recover tokens or
  namespaces. Not wired in yet.
- **Deprecation surfacing in the UI.** The `deprecated_at`/`deprecated_reason`
  columns exist in the schema already; nothing sets or displays them yet. A
  `module:deprecate` permission will arrive with the endpoint.
- **Alpine.js** for local UI component state — the current UI only needs full
  page navigation, so it's not included yet.

And one non-goal: **user accounts, passwords, and SSO/OIDC.** Tokens are the
only credential by design, to keep setup to a single binary with no identity
system to run.

## Project layout

```
cmd/coralline/          entrypoint + CLI subcommands
app/domain/              shared types (tokens, permissions, tags) and sentinel errors
app/config/              YAML config loading, zero-config defaults
app/storage/             BlobStore interface + filesystem implementation
app/sqlitestore/         repository layer, migrations, two-phase publish's DB side
app/auth/                tokens, argon2id, minting, admin bootstrap, rate limiting
app/modules/             the publish coordinator + reaper (CQRS-lite "write" side)
app/tlsutil/             self-signed certificate generation
app/transport/           terraform protocol, admin API — thin adapters
app/httpserver/          route wiring, browser sessions, health endpoints, TLS
app/logging/             structured logging, correlation IDs, audit events
ui/                      browse UI — templates (layout, shell, components, pages), component CSS/JS, handlers
```
