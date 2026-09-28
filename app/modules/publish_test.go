package modules

import (
	"context"
	"io"
	"strings"
	"testing"

	"coralline/app/storage"
)

func TestPromoteClosesStagingFileBeforeDelete(t *testing.T) {
	store, err := storage.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const stageKey = "_staging/test.tar.gz"
	const finalKey = "acme/vpc/aws/1.0.0.tar.gz"
	const contents = "test archive"

	if err := store.Put(ctx, stageKey, strings.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	publisher := &Publisher{Blobs: store}
	if err := publisher.promote(ctx, stageKey, finalKey); err != nil {
		t.Fatalf("promote: %v", err)
	}

	staged, err := store.Exists(ctx, stageKey)
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		t.Error("staging object still exists after promotion")
	}

	final, err := store.Get(ctx, finalKey)
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	got, err := io.ReadAll(final)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents {
		t.Fatalf("final contents = %q, want %q", got, contents)
	}
}
