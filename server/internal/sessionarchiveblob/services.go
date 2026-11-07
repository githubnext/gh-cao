package sessionarchiveblob

import (
	"context"
	"errors"
	"io"
)

var (
	ErrObjectExists = errors.New("archive object already exists")
	ErrNotFound     = errors.New("archive object was not found")
)

// BlobStore owns one approved private object root. Immutable writes verify the
// complete digest; pointer updates preserve ETag and lease preconditions.
type BlobStore interface {
	CheckPrivate(context.Context) error
	Read(context.Context, string, int64) ([]byte, string, error)
	WriteImmutable(context.Context, string, io.Reader, int64, string) error
	Verify(context.Context, string, string, int64) error
	CreatePointer(context.Context, []byte) error
	ComparePointer(context.Context, []byte, string, string) (string, error)
	Lease(context.Context, string, string) error
	Delete(context.Context, string) error
}

// QueryService returns complete, bounded results from the approved ADX database.
// Management supports existing metadata reads, not runtime provisioning.
type QueryService interface {
	Query(context.Context, string) (QueryResult, error)
	Management(context.Context, string) (QueryResult, error)
}

type Services struct {
	Blob    BlobStore
	Queries QueryService
}

func (s Services) validate() error {
	if s.Blob == nil || s.Queries == nil {
		return errors.New("hosted archive requires both Blob and ADX query services")
	}
	return nil
}

func azureServices(c Config) Services {
	return Services{
		Blob:    &blobClient{root: c.blobRoot(), http: newHTTP(c, "https://storage.azure.com/.default")},
		Queries: &adxQueryClient{config: c, http: newHTTP(c, "https://kusto.kusto.windows.net/.default")},
	}
}

var _ BlobStore = (*blobClient)(nil)
var _ QueryService = (*adxQueryClient)(nil)
