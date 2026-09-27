package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FSStore is the default, zero-configuration BlobStore backend. It is
// what makes `coralline serve` with no config a working registry.
type FSStore struct {
	root string
}

func NewFSStore(root string) (*FSStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("creating blob store root: %w", err)
	}
	return &FSStore{root: abs}, nil
}

// resolve maps an opaque key to a filesystem path, and enforces that the
// resulting path stays under root — belt-and-braces against traversal
// beyond what caller-side key validation (SR-1.1) already checks.
func (f *FSStore) resolve(key string) (string, error) {
	cleanKey := filepath.Clean("/" + key) // force leading slash, collapse ..
	full := filepath.Join(f.root, cleanKey)
	rel, err := filepath.Rel(f.root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid key %q: escapes storage root", key)
	}
	return full, nil
}

func (f *FSStore) Put(ctx context.Context, key string, r io.Reader) error {
	path, err := f.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Write to a temp file in the same directory, then rename — atomic
	// on the same filesystem, so a crash mid-write never leaves a
	// partially-written object at the final path.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if the rename below succeeds

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (f *FSStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	path, err := f.resolve(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (f *FSStore) Exists(ctx context.Context, key string) (bool, error) {
	path, err := f.resolve(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (f *FSStore) List(ctx context.Context, prefix string) ([]string, error) {
	base, err := f.resolve(prefix)
	if err != nil {
		return nil, err
	}
	var keys []string
	// Walk from the deepest existing directory containing the prefix.
	walkRoot := base
	if info, err := os.Stat(walkRoot); err != nil || !info.IsDir() {
		walkRoot = filepath.Dir(base)
	}
	err = filepath.Walk(walkRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasPrefix(p, base) {
			return nil
		}
		rel, err := filepath.Rel(f.root, p)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (f *FSStore) Delete(ctx context.Context, key string) error {
	path, err := f.resolve(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

var _ BlobStore = (*FSStore)(nil)
