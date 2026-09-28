package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
)

func runNamespace(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coralline namespace <create|list> ...")
	}

	fs := flag.NewFlagSet("namespace", flag.ExitOnError)
	adminAddr := fs.String("admin-addr", "127.0.0.1:8444", "admin API address")
	adminCred := fs.String("admin-cred", "", "admin credential (or set CORALLINE_ADMIN_CREDENTIAL)")

	sub := args[0]
	rest := args[1:]
	if err := fs.Parse(rest); err != nil {
		return err
	}

	cred, err := adminCredential(*adminCred)
	if err != nil {
		return err
	}
	client := &adminClient{addr: *adminAddr, cred: cred}

	switch sub {
	case "create":
		positional := fs.Args()
		if len(positional) < 1 {
			return fmt.Errorf("usage: coralline namespace create <name>")
		}
		resp, err := client.do("POST", "/namespaces", map[string]string{"name": positional[0]})
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := checkStatus(resp, 201); err != nil {
			return err
		}
		fmt.Printf("namespace %q created\n", positional[0])
		return nil

	case "list":
		resp, err := client.do("GET", "/namespaces", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := checkStatus(resp, 200); err != nil {
			return err
		}
		b, _ := io.ReadAll(resp.Body)
		var out []map[string]any
		_ = json.Unmarshal(b, &out)
		for _, n := range out {
			fmt.Printf("%s\tcreated %v\n", n["Name"], n["CreatedAt"])
		}
		return nil

	default:
		return fmt.Errorf("unknown namespace subcommand: %s", sub)
	}
}
