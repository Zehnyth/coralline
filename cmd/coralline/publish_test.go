package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeFile creates path (and its parent directories) under dir with the
// given content.
func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nopWriteCloser adapts a bytes.Buffer (or any io.Writer) to io.WriteCloser
// for packageDir, which owns closing the real pipe in production use.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// tarNames lists the file entries (not directories) in a tar.gz stream, in
// the order they appear.
func tarNames(t *testing.T, archive []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestPackageDirSkipsToolDirsAndHiddenFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `resource "null_resource" "x" {}`)
	writeFile(t, dir, "README.md", "# module")
	writeFile(t, dir, "examples/basic/main.tf", "# example")
	writeFile(t, dir, ".terraform/providers/lock.json", "should not appear")
	writeFile(t, dir, ".git/HEAD", "ref: refs/heads/main")
	writeFile(t, dir, ".terraform.lock.hcl", "should not appear")
	writeFile(t, dir, "module.tar.gz", "a leftover archive, should not appear")

	var buf bytes.Buffer
	if err := packageDir(dir, nopWriteCloser{&buf}); err != nil {
		t.Fatal(err)
	}

	names := tarNames(t, buf.Bytes())
	sort.Strings(names)
	want := []string{"README.md", "examples/basic/main.tf", "main.tf"}
	if len(names) != len(want) {
		t.Fatalf("archive contains %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("archive contains %v, want %v", names, want)
		}
	}
}
