// publish packages a module directory and publishes it to a running
// registry over HTTPS. Unlike the admin subcommands (namespace, token,
// admin), it talks to the public listener, not the loopback admin API — the
// same PUT /v1/modules/{ns}/{name}/{system}/{version}/publish endpoint the
// registry protocol handlers document, just with the archive built for you
// instead of a hand-run `tar` + `curl`.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// skippedDirs are never part of a published module: build/tool state, not
// module content.
var skippedDirs = map[string]bool{".terraform": true, ".git": true, ".idea": true, ".vscode": true}

func runPublish(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	dir := fs.String("dir", ".", "directory to package (defaults to the current directory)")
	registry := fs.String("registry", envOr("CORALLINE_REGISTRY", "https://localhost:8443"), "registry base URL (or set CORALLINE_REGISTRY)")
	token := fs.String("token", os.Getenv("CORALLINE_TOKEN"), "publish token (or set CORALLINE_TOKEN)")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification (only for an untrusted dev cert)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 4 {
		return fmt.Errorf("usage: coralline publish <namespace> <name> <system> <version> [--dir path] [--registry url] [--token token]")
	}
	ns, name, system, version := positional[0], positional[1], positional[2], positional[3]
	if *token == "" {
		return fmt.Errorf("publish token required: set CORALLINE_TOKEN or pass --token")
	}

	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	fmt.Printf("Packaging %s ...\n", root)

	// packageDir runs in its own goroutine and streams straight into the
	// request body via the pipe, so nothing needs to buffer the whole
	// archive (or write a temp file) first.
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(packageDir(root, pw)) }()

	url := strings.TrimRight(*registry, "/") + fmt.Sprintf("/v1/modules/%s/%s/%s/%s/publish", ns, name, system, version)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+*token)
	req.Header.Set("Content-Type", "application/gzip")

	client := &http.Client{}
	if *insecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // explicit opt-in, dev use only
	}

	fmt.Printf("Publishing %s/%s/%s %s to %s ...\n", ns, name, system, version, *registry)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("publish request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("publish failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out struct {
		Namespace, Name, System, Version, SHA256 string
		Size                                     int64
	}
	if err := json.Unmarshal(body, &out); err != nil {
		fmt.Println(strings.TrimSpace(string(body)))
		return nil
	}
	fmt.Printf("published %s/%s/%s %s (%d bytes, sha256:%s)\n", out.Namespace, out.Name, out.System, out.Version, out.Size, out.SHA256)
	return nil
}

// packageDir tars and gzips dir's contents into w, recursively, skipping
// tool/VCS directories, dotfiles, and any archive already sitting in the
// module (a leftover from a manual `tar` run). The result is what a
// registry module normally ships: .tf files, README, examples, and so on.
func packageDir(dir string, w io.WriteCloser) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".tar.gz") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gz.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
