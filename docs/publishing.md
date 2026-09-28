# Publishing modules

`coralline publish` packages a module directory and uploads it to the registry in one
step; no separate archiving is required.

## 1. Build the CLI

From the repository root:

```bash
go build -o bin\coralline.exe ./cmd/coralline    # Windows
go build -o bin/coralline ./cmd/coralline         # Linux / macOS
```

## 2. Set registry and token

The token must have publish access to the target namespace.

**Windows:**

```bash
$env:CORALLINE_REGISTRY = "https://coralline.localhost:8443"; $env:CORALLINE_TOKEN = Read-Host "Paste your Coralline token"
```

**Linux / macOS:**

```bash
export CORALLINE_REGISTRY="https://coralline.localhost:8443"; read -rsp "Paste your Coralline token: " CORALLINE_TOKEN; export CORALLINE_TOKEN; echo
```

## 3. Publish

A module is a directory of `.tf` files. Two examples are provided under
`terraform-modules/modules/`.

```bash
cd terraform-modules/modules/vpc
```

```bash
..\..\..\bin\coralline.exe publish acme vpc aws 1.0.0
```

On Linux/macOS, use `../../../bin/coralline`. Positional arguments:

| Argument | Description |
|---|---|
| `namespace` | Owning namespace; must already exist |
| `name` | Module name, e.g. `vpc`, `newrelic-dashboard` |
| `system` | Target provider, e.g. `aws`, `newrelic`, `github` |
| `version` | Semantic version, e.g. `1.0.0`, `1.1.0-beta.1` |

Options precede the positional arguments:

```bash
..\..\..\bin\coralline.exe publish --dir C:\path\to\module acme vpc aws 1.0.0
```

| Option | Default | Description |
|---|---|---|
| `--dir` | current directory | Directory to package |
| `--registry` | `$CORALLINE_REGISTRY` | Registry base URL |
| `--token` | `$CORALLINE_TOKEN` | Publish token |

`.terraform/`, `.git/`, editor directories, dotfiles, and `*.tar.gz` are excluded from the
package.

Versions are immutable: publishing an existing version returns `409 Conflict`. Publish an
incremented version (e.g. `1.0.1`) instead.

## 4. Use the module

```hcl
module "vpc" {
  source  = "coralline.localhost:8443/acme/vpc/aws"
  version = "1.0.0"

  # ...
}
```

```bash
tofu init
```

Expect a `Downloading coralline.localhost:8443/acme/vpc/aws 1.0.0` line. The example
project under `terraform-modules/consumer/` demonstrates this.

After publishing a new version, update `version` and run:

```bash
tofu init -upgrade
```

The web UI's **Modules** section lists every published module with a copyable source
snippet.
