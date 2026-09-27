// Package modules holds the coordinators for operations that span both
// SQLite and blob storage — the CQRS-lite "write" side (PRD §6.2). Pure
// reads live directly in sqlitestore with no indirection through here;
// only side-effect-coordinating operations get a dedicated coordinator.
package modules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"time"

	"coralline/app/domain"
	"coralline/app/storage"
)

// VersionRepo is the narrow slice of sqlitestore.DB the coordinator
// needs — kept as an interface so this package has no import-cycle
// dependency on the concrete repository.
type VersionRepo interface {
	ReserveVersion(ctx context.Context, namespace, name, system, version, tokenID string) error
	ActivateVersion(ctx context.Context, namespace, name, system, version, sha256 string, size int64) error
	FailVersion(ctx context.Context, namespace, name, system, version string) error
	TimedOutReservations(ctx context.Context, olderThan time.Time) ([]domain.Version, error)
	NamespaceExists(ctx context.Context, name string) (bool, error)
}

// defaultMaxUpload bounds an upload when the caller configures no limit.
const defaultMaxUpload = 100 * 1024 * 1024

type Publisher struct {
	Repo  VersionRepo
	Blobs storage.BlobStore
	Log   *slog.Logger
	// MaxUpload is the largest archive accepted, in bytes; zero means
	// defaultMaxUpload. An upload over the limit fails with
	// domain.ErrTooLarge rather than being truncated.
	MaxUpload int64
}

const stagingPrefix = "_staging/"

func stagingKey(namespace, name, system, version string) string {
	return fmt.Sprintf("%s%s-%s-%s-%s-%d.tar.gz", stagingPrefix, namespace, name, system, version, time.Now().UnixNano())
}

func finalKey(namespace, name, system, version string) string {
	return fmt.Sprintf("%s/%s/%s/%s.tar.gz", namespace, name, system, version)
}

// Publish runs the full two-phase protocol (PRD FR-2.4–FR-2.7):
//
//  1. Reserve the row in SQLite — the UNIQUE constraint gives atomic
//     conflict detection before any blob is touched. This is what closes
//     the race an earlier blob-first design was vulnerable to: two
//     concurrent publishes of the identical version can no longer result
//     in a winning index row pointing at a blob the loser deleted.
//  2. Stream the upload to a staging key, never directly to the final
//     path, computing a SHA-256 in the same pass.
//  3. Atomically promote staging -> final (a rename, on this filesystem
//     backend).
//  4. Activate the row. Only active versions are visible on reads.
//
// If anything fails after the reservation, the row is marked failed
// and the reaper (see reaper.go) cleans up any orphaned staging blob.
func (p *Publisher) Publish(ctx context.Context, namespace, name, system, version, tokenID string, body io.Reader) (sha256hex string, size int64, err error) {
	// Coordinates become blob keys, so they are validated here as well as
	// in the transport — this is the last point before storage is touched.
	if err := domain.ValidateModuleCoordinates(namespace, name, system, version); err != nil {
		return "", 0, err
	}

	exists, err := p.Repo.NamespaceExists(ctx, namespace)
	if err != nil {
		return "", 0, err
	}
	if !exists {
		return "", 0, domain.ErrNotFound
	}

	// Phase 1: reserve.
	if err := p.Repo.ReserveVersion(ctx, namespace, name, system, version, tokenID); err != nil {
		return "", 0, err // domain.ErrConflict on a duplicate version
	}

	fail := func(cause error) (string, int64, error) {
		// Detached from cancellation: a client that disconnects mid-upload
		// cancels ctx, and the row must still be released for a retry.
		if ferr := p.Repo.FailVersion(context.WithoutCancel(ctx), namespace, name, system, version); ferr != nil {
			p.Log.Error("failed to mark version failed after publish error", "error", ferr)
		}
		return "", 0, cause
	}

	// Phase 2: stage, hashing as we stream — a single read, teed into
	// the hash and the blob writer simultaneously (PRD FR-2.10).
	stage := stagingKey(namespace, name, system, version)
	hasher := sha256.New()
	tee := io.TeeReader(body, hasher)

	// Bound the read defensively even though the HTTP layer already
	// caps the request body (SR-2.1) — belt and braces. Reading one byte
	// past the limit is what distinguishes an oversized upload from one
	// that is exactly the limit; a bare LimitReader would silently
	// truncate it and publish a corrupt archive.
	maxUpload := p.MaxUpload
	if maxUpload <= 0 {
		maxUpload = defaultMaxUpload
	}
	counting := &countingReader{r: io.LimitReader(tee, maxUpload+1)}
	if err := p.Blobs.Put(ctx, stage, counting); err != nil {
		_ = p.Blobs.Delete(ctx, stage)
		return fail(fmt.Errorf("staging upload: %w", err))
	}
	if counting.n > maxUpload {
		_ = p.Blobs.Delete(ctx, stage)
		return fail(fmt.Errorf("%w: archive exceeds %d bytes", domain.ErrTooLarge, maxUpload))
	}

	// Phase 3: atomic promote.
	final := finalKey(namespace, name, system, version)
	if promoteErr := p.promote(ctx, stage, final); promoteErr != nil {
		_ = p.Blobs.Delete(ctx, stage)
		return fail(fmt.Errorf("promoting to final key: %w", promoteErr))
	}

	sum := hex.EncodeToString(hasher.Sum(nil))

	// Phase 4: activate.
	if err := p.Repo.ActivateVersion(ctx, namespace, name, system, version, sum, counting.n); err != nil {
		return fail(fmt.Errorf("activating version: %w", err))
	}

	return sum, counting.n, nil
}

// promote moves a staged blob to its final key. On the filesystem
// backend, BlobStore.Put already writes-then-renames atomically within
// the same directory tree, so re-reading and re-writing under the
// final key is sufficient here; an S3 backend (a later milestone)
// would instead issue a conditional PutObject / CopyObject with
// If-None-Match: * per PRD FR-2.6.
func (p *Publisher) promote(ctx context.Context, stageKey, finalKey string) error {
	exists, err := p.Blobs.Exists(ctx, finalKey)
	if err != nil {
		return err
	}
	if exists {
		// Immutability (PRD FR-2.9) is enforced by the reservation, not
		// here: holding the uploading row proves no active version points
		// at this key. A blob already there is an orphan from an earlier
		// attempt that promoted but failed to activate, so it is replaced.
		p.Log.Warn("replacing orphaned blob left by a failed publish", "key", finalKey)
	}
	r, err := p.Blobs.Get(ctx, stageKey)
	if err != nil {
		return err
	}
	if err := p.Blobs.Put(ctx, finalKey, r); err != nil {
		_ = r.Close()
		return err
	}
	// Close the staging file before deleting it. Unix permits unlinking an
	// open file, but Windows does not, so deferring this close makes every
	// filesystem-backed publish fail during promotion on Windows.
	if err := r.Close(); err != nil {
		return err
	}
	return p.Blobs.Delete(ctx, stageKey)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
