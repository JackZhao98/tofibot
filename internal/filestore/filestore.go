// Package filestore is the one place attachment bytes are written and read.
// Two backends implement Store: disk (a flat directory, the behaviour before
// object storage) and s3 (any S3-compatible bucket). A key is opaque to the
// caller; the row that owns it stores the full key. Errors are types, never
// text, and never carry a URL, host or bucket name.
package filestore

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotFound    = errors.New("object not found")
	ErrUnavailable = errors.New("store unavailable") // network, 5xx, timeout
	ErrDenied      = errors.New("store denied")      // credentials refused, bucket missing
	ErrExists      = errors.New("object exists")
)

// ObjectMeta is what the bucket records beside the bytes. The file name is
// never part of it.
type ObjectMeta struct {
	Size     int64
	SHA256   string
	SafeMIME string
}

type ObjectInfo struct {
	Key  string
	Size int64
	ETag string
}

type Store interface {
	// Kind is "disk" or "s3".
	Kind() string
	// Put writes a new object. The key must not exist (ErrExists).
	Put(ctx context.Context, key string, src io.ReadSeeker, meta ObjectMeta) error
	// Open returns a seekable reader; the object is known to exist when it returns.
	Open(ctx context.Context, key string) (io.ReadSeekCloser, ObjectInfo, error)
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	// Delete treats a missing object as success.
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error
}
