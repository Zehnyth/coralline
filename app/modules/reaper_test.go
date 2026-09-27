package modules

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"coralline/app/storage"
)

func TestSweepStagingDeletesOnlyExpiredStagingBlobs(t *testing.T) {
	store, err := storage.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	old := fmt.Sprintf("%sacme-vpc-aws-1.0.0-rc.1-%d.tar.gz", stagingPrefix, now.Add(-time.Hour).UnixNano())
	fresh := stagingKey("acme", "vpc", "aws", "1.1.0")
	final := finalKey("acme", "vpc", "aws", "1.0.0")
	for _, key := range []string{old, fresh, final} {
		if err := store.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}

	sweepStaging(ctx, store, slog.New(slog.NewTextHandler(io.Discard, nil)), now.Add(-15*time.Minute))

	for key, want := range map[string]bool{old: false, fresh: true, final: true} {
		got, err := store.Exists(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s exists = %v, want %v", key, got, want)
		}
	}
}
