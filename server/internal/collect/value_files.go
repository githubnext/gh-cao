package collect

import (
	"context"
	"errors"
	"io"
	"os"
)

const localValueLockName = "value-local"

var ErrLocalValueBusy = errors.New("a local Value computation is already running")

func readLocalValueFile(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, errors.New("local Value input is not a bounded regular file")
	}
	file, err := os.Open(path) // #nosec G304 -- confined snapshot or private output path, size checked before reading.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, errors.New("local Value input grew beyond its byte budget")
	}
	return content, ctx.Err()
}
