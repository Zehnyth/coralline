// Command coralline is the entrypoint and CLI. Admin subcommands
// (namespace, token, admin) call the running server's admin API over
// loopback rather than opening the SQLite file directly: SQLite allows
// one writer, and a second process opening the same file races with the
// running server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"coralline/app/domain"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Interrupt/SIGTERM cancel ctx, which lets serve shut down gracefully
	// and flush its buffered writes instead of dying mid-request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error

	switch os.Args[1] {
	case "serve":
		err = runServe(ctx, os.Args[2:])
	case "namespace":
		err = runNamespace(ctx, os.Args[2:])
	case "token":
		err = runToken(ctx, os.Args[2:])
	case "admin":
		err = runAdmin(ctx, os.Args[2:])
	case "publish":
		err = runPublish(ctx, os.Args[2:])
	case "version":
		fmt.Println("coralline " + domain.BuildVersion)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		stop()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`coralline — a self-hosted Terraform/OpenTofu module registry

Usage:
  coralline serve [--config path]

  coralline namespace create <name>
  coralline namespace list

  coralline token create --name <name> --namespace <ns|*> (--role reader|publisher | --grant <permission>...)
                         [--tag key=value...] [--expires-hours N]
  coralline token list   [--tag key=value] [--json]
  coralline token revoke <id>
  coralline token revoke --tag key=value --all [--yes]
  coralline token tag    <id> key=value [key=value...]
  coralline token untag  <id> key [key...]

  coralline admin create --name <operator> [--tag key=value...]
  coralline admin rotate

  coralline publish <namespace> <name> <system> <version>
                     [--dir path] [--registry url] [--token token] [--insecure]

  coralline version

Permissions: module:read, module:publish, module:* (per namespace, or * for all).
Admin commands call the loopback admin API (--admin-addr, default 127.0.0.1:8444)
with an admin token (--admin-cred, or CORALLINE_ADMIN_CREDENTIAL). publish talks
to the public registry (--registry, default https://localhost:8443, or
CORALLINE_REGISTRY) with a publish-scoped token (--token, or CORALLINE_TOKEN),
packaging --dir (default the current directory) into the archive itself. Put
flags before positional arguments.`)
}
