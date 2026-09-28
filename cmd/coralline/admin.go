package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"

	"coralline/app/auth"
)

// runAdmin manages admin tokens. These commands exist only on the loopback
// admin API: the web UI can manage standard tokens but can never mint an
// admin token, so a hijacked browser session can't grant itself lasting
// admin access.
func runAdmin(_ context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coralline admin <create|rotate> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		return adminCreate(rest)
	case "rotate":
		return adminRotate(rest)
	default:
		return fmt.Errorf("unknown admin subcommand: %s", sub)
	}
}

func adminCreate(args []string) error {
	fs := flag.NewFlagSet("admin create", flag.ExitOnError)
	name := fs.String("name", "", "operator this admin token belongs to (required)")
	var tags multiFlag
	fs.Var(&tags, "tag", "key=value label, repeatable (e.g. --tag email=alice@example.com)")
	conn := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	tagMap, err := parseTagFlags(tags)
	if err != nil {
		return err
	}
	client, err := conn.client()
	if err != nil {
		return err
	}
	var out mintedJSON
	if err := client.call("POST", "/tokens", map[string]any{"kind": "admin", "name": *name, "tags": tagMap}, 201, &out); err != nil {
		return err
	}
	fmt.Printf("Admin token %q created (id %s). It is shown once — store it now:\n", out.Name, out.ID)
	fmt.Println(out.Token)
	return nil
}

// adminRotate mints a replacement for the admin token in use (same name and
// tags), prints it, and only then revokes the old one — the new plaintext
// is never lost to a failure partway through.
func adminRotate(args []string) error {
	fs := flag.NewFlagSet("admin rotate", flag.ExitOnError)
	conn := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := conn.client()
	if err != nil {
		return err
	}
	oldID, _, ok := auth.ParseToken(client.cred)
	if !ok {
		return fmt.Errorf("the admin credential is not a Coralline token (crl_pat_...)")
	}

	var current tokenJSON
	if err := client.call("GET", "/tokens/"+url.PathEscape(oldID), nil, 200, &current); err != nil {
		return err
	}
	var out mintedJSON
	if err := client.call("POST", "/tokens", map[string]any{"kind": "admin", "name": current.Name, "tags": current.Tags}, 201, &out); err != nil {
		return err
	}
	fmt.Printf("New admin token %q (id %s). It is shown once — store it and update CORALLINE_ADMIN_CREDENTIAL:\n", out.Name, out.ID)
	fmt.Println(out.Token)

	if err := client.call("DELETE", "/tokens/"+url.PathEscape(oldID), nil, 204, nil); err != nil {
		return fmt.Errorf("the new token above works, but the old token %s is still active and could not be revoked: %w", oldID, err)
	}
	fmt.Printf("Old admin token %s revoked.\n", oldID)
	return nil
}
