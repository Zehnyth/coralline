// Package storage defines the narrow BlobStore abstraction used for both
// module artifacts and (in a later milestone) database backups. Keys are
// opaque strings; slashes are convention, not a directory requirement —
// this keeps filesystem and S3 implementations semantically identical.
package storage

import (
	"context"
	"io"
)

type BlobStore interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Exists(ctx context.Context, key string) (bool, error)
	List(ctx context.Context, prefix string) ([]string, error)
	Delete(ctx context.Context, key string) error
}
