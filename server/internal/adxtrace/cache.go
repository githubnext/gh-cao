package adxtrace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

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
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 32<<20)
		var content []byte
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return err
			}
			var item struct {
				Kind string         `json:"kind"`
				Run  map[string]any `json:"run"`
			}
			d := json.NewDecoder(strings.NewReader(scanner.Text()))
			d.UseNumber()
			if err := d.Decode(&item); err != nil {
				_ = file.Close()
				return errors.New("invalid trace acquisition cache")
			}
			id := scalar(item.Run["run_id"])
			if id == "" {
				id = scalar(item.Run["database_id"])
			}
			if slices.Contains(w.spool.config.Repositories, repository) && item.Kind == "run" && !known[id+":"+scalar(item.Run["run_attempt"])] {
				continue
			}
			if len(content)+len(scanner.Bytes())+1 > 64<<20 {
				_ = file.Close()
				return errors.New("trace acquisition cache exceeds file budget")
			}
			content = append(content, scanner.Bytes()...)
			content = append(content, '\n')
		}
		scanErr := scanner.Err()
		_ = file.Close()
		if scanErr != nil {
			return scanErr
		}
		if err := atomicWrite(target, entry.Name(), content); err != nil {
			return err
		}
	}
	return nil
}
