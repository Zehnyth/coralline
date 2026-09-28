package modules

import (
	"context"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	"coralline/app/storage"
)

// RunReaper periodically marks timed-out "uploading" reservations as
// failed and deletes staging blobs older than the same timeout — ones a
// crashed or abandoned publish never promoted.
func RunReaper(ctx context.Context, repo VersionRepo, blobs storage.BlobStore, logger *slog.Logger, interval, timeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-timeout)
			failStaleReservations(ctx, repo, logger, cutoff)
			sweepStaging(ctx, blobs, logger, cutoff)
		}
	}
}

func failStaleReservations(ctx context.Context, repo VersionRepo, logger *slog.Logger, cutoff time.Time) {
	stale, err := repo.TimedOutReservations(ctx, cutoff)
	if err != nil {
		logger.Error("reaper: listing timed-out reservations failed", "error", err)
		return
	}
	for _, v := range stale {
		if err := repo.FailVersion(ctx, v.Namespace, v.Name, v.System, v.Version); err != nil {
			logger.Error("reaper: failing stale reservation", "error", err,
				"namespace", v.Namespace, "name", v.Name, "system", v.System, "version", v.Version)
			continue
		}
		logger.Warn("reaper: failed a stale publish reservation",
			"namespace", v.Namespace, "name", v.Name, "system", v.System, "version", v.Version)
	}
}

// sweepStaging deletes staging blobs created before cutoff. The creation
// time is the nanosecond suffix stagingKey puts on every key; anything
// without one (such as a Put's in-progress temp file) is left alone.
func sweepStaging(ctx context.Context, blobs storage.BlobStore, logger *slog.Logger, cutoff time.Time) {
	keys, err := blobs.List(ctx, stagingPrefix)
	if err != nil {
		logger.Error("reaper: listing staging blobs failed", "error", err)
		return
	}
	for _, key := range keys {
		created, ok := stagingKeyTime(key)
		if !ok || !created.Before(cutoff) {
			continue
		}
		if err := blobs.Delete(ctx, key); err != nil {
			logger.Error("reaper: deleting orphaned staging blob", "error", err, "key", key)
			continue
		}
		logger.Warn("reaper: deleted an orphaned staging blob", "key", key)
	}
}

func stagingKeyTime(key string) (time.Time, bool) {
	base, ok := strings.CutSuffix(path.Base(key), ".tar.gz")
	if !ok {
		return time.Time{}, false
	}
	i := strings.LastIndexByte(base, '-')
	if i < 0 {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(base[i+1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}
