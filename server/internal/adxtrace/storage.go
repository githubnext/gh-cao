package adxtrace

import (
	"context"
	"errors"
	"io/fs"
	"os"
)

// Storage serializes a destination's ledger and approved immutable objects in
// one durable transaction. Implementations must roll back every object mutation
// when action fails, and release ownership on cancellation or process failure.
// Callbacks may perform idempotent remote delivery before committing its receipt.
type Storage interface {
	Transaction(context.Context, func(Objects) error) error
}

type ObjectInfo struct {
	Key   string
	Bytes int64
}

// Objects is valid only inside its owning Storage transaction. Read must bound
// allocation before fetching content; missing objects return fs.ErrNotExist.
type Objects interface {
	Read(string, int64) ([]byte, error)
	Write(string, []byte) error
	Remove(string) error
	List() ([]ObjectInfo, error)
}

type localObjects struct{ root *os.Root }

func (o localObjects) Read(key string, limit int64) ([]byte, error) {
	return readPrivateObject(o.root, key, limit)
}

func (o localObjects) Write(key string, content []byte) error {
	return writePrivateObject(o.root, key, content)
}

func (o localObjects) Remove(key string) error { return o.root.Remove(key) }

func (o localObjects) List() ([]ObjectInfo, error) {
	entries, err := fs.ReadDir(o.root.FS(), ".")
	if err != nil {
		return nil, err
	}
	result := make([]ObjectInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("trace object is not a private regular file")
		}
		result = append(result, ObjectInfo{Key: entry.Name(), Bytes: info.Size()})
	}
	return result, nil
}

func readBounded(objects Objects, key string, limit int64) ([]byte, error) {
	return objects.Read(key, limit)
}

func atomicWrite(objects Objects, key string, content []byte) error {
	return objects.Write(key, content)
}
