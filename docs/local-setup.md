# Local setup

Runs a Coralline registry on `https://coralline.localhost:8443`.

**Requirements:** [Podman](https://podman.io) (or Docker), and [OpenTofu](https://opentofu.org)
or Terraform. Commands are run from the repository root.

> Steps marked *(admin)* require an elevated shell: PowerShell run as administrator on
> Windows, `sudo` on Linux/macOS.

## 1. Start the server

```bash
podman build -t coralline:latest .
```

```bash
podman run -d --name coralline -p 8443:8443 -v C:\path\to\coralline\data:/coralline/data coralline:latest
```

Substitute the repository's actual path for `-v`; this persists data across container
restarts.

Confirm the server started:

```bash
podman logs coralline
```

Expect a `coralline listening` line. To apply a code change, remove and recreate the
container:

```bash
podman rm -f coralline
```

Then rerun the `build` and `run` commands. Data under `./data` is unaffected.

## 2. Record the admin token

On first start with no active admin token, the server generates one and prints it once:

```bash
podman logs coralline
```

If it is lost, revoke every admin token and restart the server — a new one is issued
whenever no admin token is active.

## 3. Configure a dotted hostname

OpenTofu and Terraform reject registry hostnames without a dot, so `localhost:8443` cannot
be used as a module source host. `coralline.localhost` is used instead.

**Windows** *(admin)*. Podman for Windows listens on `::1`:

```bash
Add-Content -Path C:\Windows\System32\drivers\etc\hosts -Value "::1 coralline.localhost"
```

**Linux / macOS.** Many systems resolve `*.localhost` without configuration:

```bash
getent hosts coralline.localhost
```

If that returns nothing, add an entry:

```bash
echo "127.0.0.1 coralline.localhost" | sudo tee -a /etc/hosts
```

## 4. Trust the development certificate

The registry protocol requires HTTPS, and `tofu init` has no option to skip certificate
verification. The server generates a self-signed certificate at `data/tls/dev-cert.pem` on
first start; it must be trusted by the client machine.

**Windows** *(admin)*:

```bash
Import-Certificate -FilePath .\data\tls\dev-cert.pem -CertStoreLocation Cert:\LocalMachine\Root
```

**Ubuntu / Debian:**

```bash
sudo cp data/tls/dev-cert.pem /usr/local/share/ca-certificates/coralline-dev.crt && sudo update-ca-certificates
```

**Fedora / RHEL:**

```bash
sudo cp data/tls/dev-cert.pem /etc/pki/ca-trust/source/anchors/coralline-dev.crt && sudo update-ca-trust
```

**macOS:**

```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain data/tls/dev-cert.pem
```

Deleting `data/tls/` generates a new certificate on the next start, which must be trusted
again.

This certificate is for local development only. Production deployments require a certificate
already trusted by client machines, or a reverse proxy (Caddy, Traefik, nginx) terminating
TLS with one.

## 5. Sign in

Open `https://coralline.localhost:8443` and sign in with the admin token from step 2.

## 6. Create a namespace

A namespace groups related modules, typically by team (`acme`, `platform`). Namespaces are
created via the CLI, running inside the container with the admin token:

```bash
podman exec -e CORALLINE_ADMIN_CREDENTIAL="<admin token>" coralline /usr/local/bin/coralline namespace create acme
```

## 7. Create a personal token

The admin token should not be used for routine work. In the web UI:

1. Open **Tokens → New token**.
2. Name it to identify its owner, e.g. `alice-laptop`.
3. Set access to **Publisher** for the `acme` namespace.
4. Copy the token — it is shown once.

This token is used in the following guides.

**Next:** [Connecting OpenTofu / Terraform](connect-opentofu.md)
