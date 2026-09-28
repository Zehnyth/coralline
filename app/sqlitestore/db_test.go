package sqlitestore

import (
	"context"
	"path/filepath"
	"testing"
)

// The DB path is spliced into a URI-style DSN, so characters that would
// change its meaning are refused rather than escaped.
func TestOpenRejectsPathsThatAlterTheDSN(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{
		"",
		filepath.Join(dir, "index.db?_pragma=foreign_keys(0)"),
		filepath.Join(dir, "index.db#fragment"),
		filepath.Join(dir, "index%2Edb"),
	} {
		if db, err := Open(path); err == nil {
			db.Close()
			t.Errorf("Open(%q) succeeded, want an error", path)
		}
	}
}

func TestOpenAppliesPragmas(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var fk int
	if err := db.sql.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v; want 1", fk, err)
	}
}
