package modules_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"coralline/app/domain"
	"coralline/app/modules"
	"coralline/app/sqlitestore"
	"coralline/app/storage"
)

func newTestPublisher(t *testing.T) (*modules.Publisher, *sqlitestore.DB, *storage.FSStore) {
	t.Helper()
	root := t.TempDir()
	db, err := sqlitestore.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.CreateNamespace(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}

	blobs, err := storage.NewFSStore(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	publisher := &modules.Publisher{
		Repo:  db,
		Blobs: blobs,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return publisher, db, blobs
}

func TestPublishActivatesVersionWithSQLiteAndFSStore(t *testing.T) {
	publisher, db, _ := newTestPublisher(t)

	if _, _, err := publisher.Publish(
		context.Background(), "acme", "vpc", "aws", "1.0.0", "test-token", strings.NewReader("archive"),
	); err != nil {
		t.Fatalf("publish: %v", err)
	}
	versions, err := db.ListActiveVersions(context.Background(), "acme", "vpc", "aws")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != "1.0.0" {
		t.Fatalf("active versions = %v, want [1.0.0]", versions)
	}
}

func TestPublishRejectsInvalidCoordinatesBeforeStorage(t *testing.T) {
	publisher, _, blobs := newTestPublisher(t)
	ctx := context.Background()

	_, _, err := publisher.Publish(ctx, "acme", "../../other/mod", "aws", "1.0.0", "test-token", strings.NewReader("archive"))
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("publish err = %v, want ErrInvalid", err)
	}
	keys, err := blobs.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("blob store has %v after a rejected publish, want nothing", keys)
	}
}

func TestPublishCanRetryAFailedVersion(t *testing.T) {
	publisher, db, _ := newTestPublisher(t)
	ctx := context.Background()

	if err := db.ReserveVersion(ctx, "acme", "vpc", "aws", "1.0.0", "test-token"); err != nil {
		t.Fatal(err)
	}
	if err := db.FailVersion(ctx, "acme", "vpc", "aws", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := publisher.Publish(ctx, "acme", "vpc", "aws", "1.0.0", "test-token", strings.NewReader("archive")); err != nil {
		t.Fatalf("retrying a failed version: %v", err)
	}
	if _, err := db.GetActiveVersion(ctx, "acme", "vpc", "aws", "1.0.0"); err != nil {
		t.Fatalf("version not active after retry: %v", err)
	}

	_, _, err := publisher.Publish(ctx, "acme", "vpc", "aws", "1.0.0", "test-token", strings.NewReader("other"))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("republishing an active version err = %v, want ErrConflict", err)
	}
}

func TestPublishRejectsOversizedArchiveWithoutTruncating(t *testing.T) {
	publisher, db, _ := newTestPublisher(t)
	publisher.MaxUpload = 4
	ctx := context.Background()

	if _, _, err := publisher.Publish(ctx, "acme", "vpc", "aws", "1.0.0", "test-token", strings.NewReader("12345")); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("publish err = %v, want ErrTooLarge", err)
	}
	if _, err := db.GetActiveVersion(ctx, "acme", "vpc", "aws", "1.0.0"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("oversized archive was activated (err = %v)", err)
	}

	// Exactly at the limit is accepted, and the failed attempt above
	// doesn't block the version.
	if _, size, err := publisher.Publish(ctx, "acme", "vpc", "aws", "1.0.0", "test-token", strings.NewReader("1234")); err != nil || size != 4 {
		t.Fatalf("publish at limit: size = %d, err = %v", size, err)
	}
}
