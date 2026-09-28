# Connecting OpenTofu / Terraform

OpenTofu and Terraform read registry credentials from a CLI configuration file. Once a
token is added to it, `tofu init` authenticates to Coralline automatically.

> `tofu login` / `terraform login` are not supported. Both require an OAuth login server;
> Coralline is token-only by design. The credentials file serves the same purpose.

## 1. Locate the configuration file

| OS | File | Notes |
|---|---|---|
| Windows | `%APPDATA%\terraform.rc` | OpenTofu prefers `%APPDATA%\tofu.rc` if present |
| Linux / macOS | `~/.terraformrc` | OpenTofu prefers `~/.tofurc` if present |

`terraform.rc` / `.terraformrc` is read by both tools and is used throughout this guide.

## 2. Add a credentials block

The label must match the module source host exactly, including the port:

```hcl
credentials "coralline.localhost:8443" {
  token = "crl_pat_<token>"
}
```

### Windows

Edit the file directly:

```bash
notepad "$env:APPDATA\terraform.rc"
```

Or create it from the shell:

```bash
$tok = Read-Host "Paste your Coralline token"; [IO.File]::WriteAllText("$env:APPDATA\terraform.rc", "credentials `"coralline.localhost:8443`" {`n  token = `"$tok`"`n}`n", (New-Object Text.UTF8Encoding($false)))
```

Do not create this file with `Set-Content -Encoding utf8` or `Out-File` — both add a byte
order mark, and OpenTofu fails to parse the file with `illegal char`.

### Linux / macOS

```bash
cat > ~/.terraformrc <<'EOF'
credentials "coralline.localhost:8443" {
  token = "crl_pat_<token>"
}
EOF
```

The file contains a secret; restrict its permissions:

```bash
chmod 600 ~/.terraformrc
```

## 3. Verify

Confirm the file exists:

```bash
Get-Content "$env:APPDATA\terraform.rc"      # Windows
cat ~/.terraformrc                           # Linux / macOS
```

Query the registry with the same token. `200` indicates the token is valid; `404` also
indicates a valid token, for a module that has not been published yet.

**Windows:**

```bash
$tok = (Select-String -Path "$env:APPDATA\terraform.rc" -Pattern 'token\s*=\s*"([^"]+)"').Matches[0].Groups[1].Value; curl.exe -s -o NUL -w "%{http_code}`n" -H "Authorization: Bearer $tok" https://coralline.localhost:8443/v1/modules/acme/vpc/aws/versions
```

**Linux / macOS:**

```bash
tok=$(sed -nE 's/.*token *= *"([^"]+)".*/\1/p' ~/.terraformrc); curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $tok" https://coralline.localhost:8443/v1/modules/acme/vpc/aws/versions
```

`401` indicates the token is invalid, revoked, or expired — see
[Troubleshooting](troubleshooting.md).

## Alternatives to the credentials file

- **`TF_CLI_CONFIG_FILE`** — points to a config file at a custom path. Suited to CI.
- **`TF_TOKEN_<host>`** — an environment variable holding the token, with dots in the
  hostname replaced by underscores (e.g. `TF_TOKEN_registry_example_com`). This form cannot
  encode a port, so it only applies to a registry on the default port 443. For
  `coralline.localhost:8443`, use the credentials file.

**Next:** [Publishing modules](publishing.md)
