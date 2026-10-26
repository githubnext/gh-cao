package collect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// TraceCaptures shares one authorized acquisition between independently enabled
// destinations. Cache entries survive only when every destination admitted them.
type TraceCaptures []TraceCapture

func (captures TraceCaptures) PrepareCache(ctx context.Context, repository, source, target string) error {
	for i, capture := range captures {
		next := target
		if i < len(captures)-1 {
			directory, err := os.MkdirTemp(filepath.Dir(target), ".capture-cache-")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(directory) }()
			next = filepath.Join(directory, filepath.Base(target))
		}
		if err := capture.PrepareCache(ctx, repository, source, next); err != nil {
			return err
		}
		source = next
	}
	return nil
}

func (captures TraceCaptures) Capture(ctx context.Context, repository, logs, prefix string) error {
	var result error
	for _, capture := range captures {
		result = errors.Join(result, capture.Capture(ctx, repository, logs, prefix))
	}
	return result
}

func (captures TraceCaptures) WithdrawRepository(ctx context.Context, repository string) error {
	var result error
	for _, capture := range captures {
		result = errors.Join(result, capture.WithdrawRepository(ctx, repository))
	}
	return result
}
