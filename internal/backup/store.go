package backup

import (
	"context"
	"io"
	"time"
)

// ObjectInfo is one stored object. LastModified is the upload time.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	SHA256       []byte
}

// Upload is one object to store. SHA256 is the raw checksum of Body when
// the caller already has it. A nil SHA256 asks the store to hash the stream.
type Upload struct {
	Key    string
	Body   io.Reader
	Size   int64
	SHA256 []byte
}

// ObjectStore is the R2 (S3) operations the backup uses. It does not create
// buckets.
type ObjectStore interface {
	Put(ctx context.Context, up Upload) error
	Head(ctx context.Context, key string) (ObjectInfo, error)
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Delete(ctx context.Context, key string) error
}
