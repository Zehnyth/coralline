# Documentation

| Guide | Contents |
|---|---|
| [Local setup](local-setup.md) | Run the server, trust the development certificate, create a namespace and a token |
| [Connecting OpenTofu / Terraform](connect-opentofu.md) | Configure CLI credentials on Windows, Linux, and macOS |
| [Publishing modules](publishing.md) | Package and publish a module with `coralline publish` |
| [Troubleshooting](troubleshooting.md) | Common errors and their causes |

New installations should follow the guides in order.

## Concepts

- **Module.** A versioned artifact identified by `namespace/name/system` (for example
  `acme/vpc/aws`). A version, once published, is immutable.
- **Token.** The only credential type in Coralline; there are no user accounts. Each token
  carries its own read/publish grants per namespace.
- **Module source address.** `<host>/<namespace>/<name>/<system>`, e.g.
  `coralline.localhost:8443/acme/vpc/aws`.

See [README.md](../README.md) for build instructions and [ARCHITECTURE.md](../ARCHITECTURE.md)
for design details.
