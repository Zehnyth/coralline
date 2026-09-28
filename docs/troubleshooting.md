# Troubleshooting

Check the server log first. Every request is logged, and every rejected authentication
states a reason:

```bash
podman logs --tail 20 coralline
```

## `401 Unauthorized` during `tofu init`

Locate the corresponding `auth rejected` line. The `reason` field identifies the cause:

| `reason` | Cause | Resolution |
|---|---|---|
| `missing_bearer` | No token was sent | Verify the credentials file exists and its host label matches exactly, including `:8443` |
| `malformed_token` | Token is truncated or corrupted | Re-copy the full token |
| `unknown_token` | Token was not issued by this server | Token belongs to a different registry instance; create a new one |
| `inactive_token` | Token is revoked or expired | Create a new token |
| `bad_secret` | Token ID is valid but the secret does not match | Re-copy the full token |

Confirm the credentials file exists:

```bash
Test-Path "$env:APPDATA\terraform.rc"    # Windows
ls -l ~/.terraformrc                      # Linux / macOS
```

If it does not, see [Connecting OpenTofu / Terraform](connect-opentofu.md#2-add-a-credentials-block).

Check for an overriding environment variable in the current shell:

```bash
Get-ChildItem env: | Where-Object Name -match '^(TF_|TOFU_)'   # Windows
env | grep -E '^(TF_|TOFU_)'                                     # Linux / macOS
```

## `403 Forbidden`

The token is valid but lacks access to the namespace. Reads require read access; publishing
requires publish access. Update the token's access under **Tokens** in the web UI.

## `invalid module registry hostname: must contain at least one dot`

`localhost:8443` was used as a module source host. Use `coralline.localhost:8443` and add
the hosts file entry described in
[Local setup, step 3](local-setup.md#3-configure-a-dotted-hostname).

## `x509: certificate signed by unknown authority`

The development certificate is not trusted by this machine. See
[Local setup, step 4](local-setup.md#4-trust-the-development-certificate). If `data/tls/`
was deleted, the server generated a new certificate that must be trusted again.

## `connection refused` on `coralline.localhost`

- Confirm the container is running: `podman ps`.
- On Windows, Podman listens on `::1`, not `127.0.0.1`. The hosts file entry must read
  `::1 coralline.localhost`.

## `Invalid character` / `illegal char` in the credentials file

The file was saved with a byte order mark — a known effect of
`Set-Content -Encoding utf8` in Windows PowerShell. Recreate it per
[Connecting OpenTofu / Terraform](connect-opentofu.md#windows).

## `409 Conflict` when publishing

The version already exists. Versions are immutable; publish an incremented version.

## `Host does not support OpenTofu tokens API` on `tofu login`

Expected — Coralline does not implement the OpenTofu tokens API. Use the credentials file
instead; see [Connecting OpenTofu / Terraform](connect-opentofu.md).
