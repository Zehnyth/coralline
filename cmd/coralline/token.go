package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"coralline/app/domain"
)

func runToken(_ context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coralline token <create|list|revoke|tag|untag> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		return tokenCreate(rest)
	case "list":
		return tokenList(rest)
	case "revoke":
		return tokenRevoke(rest)
	case "tag":
		return tokenTag(rest)
	case "untag":
		return tokenUntag(rest)
	default:
		return fmt.Errorf("unknown token subcommand: %s", sub)
	}
}

func tokenCreate(args []string) error {
	fs := flag.NewFlagSet("token create", flag.ExitOnError)
	name := fs.String("name", "", "who or what will use the token (required)")
	namespace := fs.String("namespace", "", "namespace the grants apply to, or * for all namespaces (required)")
	role := fs.String("role", "", "reader or publisher")
	var grants, tags multiFlag
	fs.Var(&grants, "grant", "permission on --namespace, repeatable: module:read, module:publish, module:*")
	fs.Var(&tags, "tag", "key=value label, repeatable (e.g. --tag owner=alice)")
	expiresHours := fs.Int("expires-hours", 0, "expire after N hours (0 = never)")
	conn := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	if *namespace == "" {
		return fmt.Errorf("--namespace is required (use * for all namespaces)")
	}

	perms := append([]string(nil), grants...)
	if *role != "" {
		expanded, err := domain.ExpandRole(*role)
		if err != nil {
			return err
		}
		for _, p := range expanded {
			perms = append(perms, string(p))
		}
	}
	if len(perms) == 0 {
		return fmt.Errorf("pass --role reader|publisher or at least one --grant")
	}
	tagMap, err := parseTagFlags(tags)
	if err != nil {
		return err
	}

	grantBody := make([]map[string]string, 0, len(perms))
	for _, p := range perms {
		grantBody = append(grantBody, map[string]string{"namespace": *namespace, "permission": p})
	}
	client, err := conn.client()
	if err != nil {
		return err
	}
	var out mintedJSON
	err = client.call("POST", "/tokens", map[string]any{
		"kind":             "standard",
		"name":             *name,
		"grants":           grantBody,
		"tags":             tagMap,
		"expires_in_hours": *expiresHours,
	}, 201, &out)
	if err != nil {
		return err
	}
	fmt.Printf("Token %q created (id %s). It is shown once — store it now:\n", out.Name, out.ID)
	fmt.Println(out.Token)
	return nil
}

func fetchTokens(client *adminClient, tag string) ([]tokenJSON, error) {
	path := "/tokens"
	if tag != "" {
		path += "?tag=" + url.QueryEscape(tag)
	}
	var out []tokenJSON
	if err := client.call("GET", path, nil, 200, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func printTokens(toks []tokenJSON) {
	now := time.Now()
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tKIND\tSTATUS\tLAST USED\tGRANTS\tTAGS")
	for _, t := range toks {
		grants := "all (admin)"
		if t.Kind != string(domain.TokenKindAdmin) {
			parts := make([]string, 0, len(t.Grants))
			for _, g := range t.Grants {
				parts = append(parts, g.Namespace+":"+g.Permission)
			}
			grants = strings.Join(parts, " ")
		}
		keys := make([]string, 0, len(t.Tags))
		for k := range t.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		tagParts := make([]string, 0, len(keys))
		for _, k := range keys {
			tagParts = append(tagParts, k+"="+t.Tags[k])
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Name, t.Kind, t.status(now), formatWhen(t.LastUsedAt), grants, strings.Join(tagParts, " "))
	}
	tw.Flush()
}

func tokenList(args []string) error {
	fs := flag.NewFlagSet("token list", flag.ExitOnError)
	tag := fs.String("tag", "", "only tokens carrying this key=value tag")
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	conn := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := conn.client()
	if err != nil {
		return err
	}
	toks, err := fetchTokens(client, *tag)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(toks)
	}
	printTokens(toks)
	return nil
}

func tokenRevoke(args []string) error {
	fs := flag.NewFlagSet("token revoke", flag.ExitOnError)
	tag := fs.String("tag", "", "revoke tokens carrying this key=value tag (requires --all)")
	all := fs.Bool("all", false, "with --tag: revoke every matching token")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	conn := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()

	switch {
	case *tag != "" && len(positional) > 0:
		return fmt.Errorf("pass either a token id or --tag, not both")

	case *tag != "":
		if !*all {
			return fmt.Errorf("--tag revokes every matching token; add --all to confirm that's what you want")
		}
		client, err := conn.client()
		if err != nil {
			return err
		}
		matches, err := fetchTokens(client, *tag)
		if err != nil {
			return err
		}
		var targets []tokenJSON
		for _, t := range matches {
			if t.RevokedAt == nil {
				targets = append(targets, t)
			}
		}
		if len(targets) == 0 {
			fmt.Printf("No unrevoked tokens are tagged %s.\n", *tag)
			return nil
		}
		printTokens(targets)
		if !*yes && !confirm(fmt.Sprintf("Revoke these %d token(s)? [y/N]: ", len(targets))) {
			fmt.Println("Aborted; nothing was revoked.")
			return nil
		}
		// Send exactly the ids shown above, so a token tagged after the
		// listing is never revoked unseen.
		ids := make([]string, 0, len(targets))
		for _, t := range targets {
			ids = append(ids, t.ID)
		}
		var result struct {
			Revoked []string `json:"revoked"`
			Failed  []struct {
				ID    string `json:"id"`
				Error string `json:"error"`
			} `json:"failed"`
		}
		if err := client.call("POST", "/tokens/revoke", map[string]any{"ids": ids}, 200, &result); err != nil {
			return err
		}
		fmt.Printf("Revoked %d token(s).\n", len(result.Revoked))
		for _, f := range result.Failed {
			fmt.Printf("  not revoked %s: %s\n", f.ID, f.Error)
		}
		if len(result.Failed) > 0 {
			return fmt.Errorf("%d token(s) could not be revoked", len(result.Failed))
		}
		return nil

	case len(positional) == 1:
		client, err := conn.client()
		if err != nil {
			return err
		}
		if err := client.call("DELETE", "/tokens/"+url.PathEscape(positional[0]), nil, 204, nil); err != nil {
			return err
		}
		fmt.Printf("Token %s revoked.\n", positional[0])
		return nil

	default:
		return fmt.Errorf("usage: coralline token revoke [flags] <id>  |  coralline token revoke --tag key=value --all [--yes]")
	}
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func tokenTag(args []string) error {
	fs := flag.NewFlagSet("token tag", flag.ExitOnError)
	conn := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 2 {
		return fmt.Errorf("usage: coralline token tag [flags] <id> key=value [key=value...]")
	}
	return editTags(conn, positional[0], func(tags map[string]string) error {
		for _, pair := range positional[1:] {
			t, err := domain.ParseTag(pair)
			if err != nil {
				return err
			}
			tags[strings.ToLower(t.Key)] = t.Value
		}
		return nil
	})
}

func tokenUntag(args []string) error {
	fs := flag.NewFlagSet("token untag", flag.ExitOnError)
	conn := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) < 2 {
		return fmt.Errorf("usage: coralline token untag [flags] <id> key [key...]")
	}
	return editTags(conn, positional[0], func(tags map[string]string) error {
		for _, key := range positional[1:] {
			delete(tags, strings.ToLower(strings.TrimSpace(key)))
		}
		return nil
	})
}

// editTags reads a token's tags, applies change, and writes the full set
// back. The server validates the result.
func editTags(conn connFlags, id string, change func(map[string]string) error) error {
	client, err := conn.client()
	if err != nil {
		return err
	}
	var tok tokenJSON
	if err := client.call("GET", "/tokens/"+url.PathEscape(id), nil, 200, &tok); err != nil {
		return err
	}
	tags := map[string]string{}
	for k, v := range tok.Tags {
		tags[k] = v
	}
	if err := change(tags); err != nil {
		return err
	}
	if err := client.call("PUT", "/tokens/"+url.PathEscape(id)+"/tags", map[string]any{"tags": tags}, 204, nil); err != nil {
		return err
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k+"="+tags[k])
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		fmt.Printf("Token %q now has no tags.\n", tok.Name)
	} else {
		fmt.Printf("Token %q tags: %s\n", tok.Name, strings.Join(keys, " "))
	}
	return nil
}
