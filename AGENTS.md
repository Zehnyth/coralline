# Repository Guidelines

## Project Structure & Module Organization

Coralline is a Go 1.22 Terraform/OpenTofu module registry. Executable and CLI code lives in `cmd/coralline/`. Code under `app/` is grouped by responsibility: domain types, configuration, storage, SQLite persistence, publishing, transports, HTTP setup, TLS, and logging. The browse UI — templates, component CSS/JS, and handlers — is its own top-level package at `ui/`, kept separate from `app/` since it only depends on shared domain types and logging.

Treat `data/` as local runtime state: it may contain SQLite files, credentials, blobs, and development certificates. Do not commit newly generated secrets or databases. Edit `config/coralline.yml` (loaded by `serve` by default) for local overrides.

## Build, Test, and Development Commands

- `go mod tidy` downloads dependencies and normalizes `go.mod`/`go.sum`.
- `go build ./...` compiles every package and catches cross-package errors.
- `go test ./...` runs the complete test suite.
- `go vet ./...` performs standard Go static checks.
- `go run ./cmd/coralline serve` starts the registry with zero-config defaults on `:8443`.
- `go run ./cmd/coralline namespace create acme` exercises an admin CLI command; set `CORALLINE_ADMIN_CREDENTIAL` first.

Run formatting, tests, and a full build before opening a pull request.

## Coding Style & Naming Conventions

Run `gofmt -w <files>` on every changed Go file. Keep package names short, lowercase, and singular. Use `MixedCaps` identifiers and concise receiver names. Keep storage details behind interfaces such as `BlobStore`. Wrap errors with context and reuse domain sentinel errors where appropriate.

## Testing Guidelines

Add tests beside implementation files as `*_test.go`, using `TestName` and table-driven cases for validation, protocol responses, and error paths. Prefer temporary directories and isolated SQLite databases; never mutate checked-in `data/`. No coverage threshold is currently configured, so changes should at least cover new behavior and regressions. Run `go test ./...` locally.

## Commit & Pull Request Guidelines

Git history is not available in this checkout. Use short, imperative commit subjects, optionally scoped, for example `auth: reject expired publish tokens`. Keep commits focused. Pull requests should explain the behavior change, configuration or migration impact, and verification commands. Link relevant issues and include screenshots for browse-UI changes or sample request/response output for protocol changes.

## Security & Configuration

Never commit admin credentials, bearer tokens, private keys, or production certificates. Self-signed TLS is for local evaluation only; production deployments require trusted TLS or a terminating reverse proxy. Preserve upload limits, path-traversal protections, and authentication boundaries when changing storage or HTTP handlers.
