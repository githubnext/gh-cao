package adxtrace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var cacheLog = logger.New("cao:adxtrace")

// PrepareCache stages metadata without uncaptured attempts. A failed capture
// never commits the CLI cache write, so retries reacquire the authorized artifact.
func (w *Worker) PrepareCache(ctx context.Context, repository, sourcePrefix, targetPrefix string) error {
	known := map[string]bool{}
	if err := w.spool.locked(ctx, func(_ *os.Root, state *ledger) error {
		for _, r := range state.Records {
			if r.Repository == repository && r.State != "source-gap" {
				known[r.RunID+":"+strconv.FormatInt(r.Attempt, 10)] = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return PrepareCaptureCache(ctx, slices.Contains(w.spool.config.Repositories, repository), sourcePrefix, targetPrefix, known)
}

// PrepareCaptureCache prevents cached metadata from suppressing unadmitted
// evidence acquisition; known keys use the exact "run:attempt" binding.
func PrepareCaptureCache(ctx context.Context, scoped bool, sourcePrefix, targetPrefix string, known map[string]bool) error {
	root, err := os.OpenRoot(filepath.Dir(sourcePrefix))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	target, err := os.OpenRoot(filepath.Dir(targetPrefix))
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), filepath.Base(sourcePrefix)) || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		file, err := root.Open(entry.Name())
		if err != nil {
			return err
		}
		content, dropped, err := filterCaptureCache(ctx, file, scoped, known)
		_ = file.Close()
		if err != nil {
			return err
		}
		cacheLog.Printf("prepared capture cache file: scoped=%t dropped_runs=%d bytes=%d", scoped, dropped, len(content))
		if err := atomicWrite(target, entry.Name(), content); err != nil {
			return err
		}
	}
	return nil
}

// filterCaptureCache copies cached JSONL lines, omitting run records whose
// "run:attempt" key is not known when the cache is scoped. It reports how many
// run records were dropped.
func filterCaptureCache(ctx context.Context, r io.Reader, scoped bool, known map[string]bool) ([]byte, int, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 32<<20)
	var content []byte
	dropped := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		var item struct {
			Kind string         `json:"kind"`
			Run  map[string]any `json:"run"`
		}
		d := json.NewDecoder(strings.NewReader(scanner.Text()))
		d.UseNumber()
		if err := d.Decode(&item); err != nil {
			return nil, 0, errors.New("invalid trace acquisition cache")
		}
		id := scalar(item.Run["run_id"])
		if id == "" {
			id = scalar(item.Run["database_id"])
		}
		if scoped && item.Kind == "run" && !known[id+":"+scalar(item.Run["run_attempt"])] {
			dropped++
			continue
		}
		if len(content)+len(scanner.Bytes())+1 > 64<<20 {
			return nil, 0, errors.New("trace acquisition cache exceeds file budget")
		}
		content = append(content, scanner.Bytes()...)
		content = append(content, '\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return content, dropped, nil
}
