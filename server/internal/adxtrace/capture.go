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
	"time"
)

type candidate struct {
	run, directory, day string
	attempt             int64
}

func sourceFile(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("trace source is not a regular file")
	}
	if info.Size() > limit {
		return nil, errSourceSize
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, errSourceSize
	}
	return content, nil
}

var errSourceSize = errors.New("trace source exceeds eligibility size")

func (w *Worker) Capture(ctx context.Context, repository, logs, shardPrefix string) error {
	if !slices.Contains(w.spool.config.Repositories, repository) {
		return nil // An explicitly narrower export scope does not widen collection.
	}
	shards, err := os.OpenRoot(filepath.Dir(shardPrefix))
	if err != nil {
		return err
	}
	defer func() { _ = shards.Close() }()
	entries, err := fs.ReadDir(shards.FS(), ".")
	if err != nil {
		return err
	}
	candidates := map[string]candidate{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), filepath.Base(shardPrefix)) || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		file, err := shards.Open(entry.Name())
		if err != nil {
			return err
		}
		err = readCandidates(ctx, file, repository, logs, candidates, w.spool.config.MaxEntries)
		_ = file.Close()
		if err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(logs)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := candidates[key]
		now := w.now()
		e := Evidence{ID: digest([]byte(repository + "\x00" + c.run + "\x00" + strconv.FormatInt(c.attempt, 10))),
			Repository: repository, RunID: c.run, Attempt: c.attempt, Day: c.day, CapturedAt: now,
			PolicyDigest: policyDigest(w.spool.config.Policy)}
		path, info, reason, err := artifactPath(root, c.directory)
		if err != nil {
			return err
		}
		if reason == "" && (scalar(info["run_id"]) != c.run ||
			scalar(info["run_attempt"]) != strconv.FormatInt(c.attempt, 10) ||
			strings.ToLower(scalar(info["repository"])) != repository) {
			reason = "artifact-identity-gap"
		}
		if reason == "" {
			content, err := sourceFile(root, path, w.spool.config.MaxTraceBytes)
			switch {
			case errors.Is(err, errSourceSize):
				reason = "size-exclusion"
			case err != nil:
				return err
			default:
				e, err = Project(w.spool.config, repository, c.run, c.day, c.attempt, content, now)
				if err != nil {
					reason = "invalid-source"
					e = Evidence{ID: digest([]byte(repository + "\x00" + c.run + "\x00" + strconv.FormatInt(c.attempt, 10))),
						Repository: repository, RunID: c.run, Attempt: c.attempt, Day: c.day, CapturedAt: now,
						PolicyDigest: policyDigest(w.spool.config.Policy)}
				}
			}
		}
		if err := w.spool.admit(ctx, e, reason, now); err != nil {
			return err
		}
	}
	return nil
}

func readCandidates(ctx context.Context, reader io.Reader, repository, logs string, out map[string]candidate, limit int) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 32<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var item struct {
			Kind string         `json:"kind"`
			Run  map[string]any `json:"run"`
		}
		// Shards carry additional envelope fields not part of trace capture.
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.UseNumber()
		if err := decoder.Decode(&item); err != nil {
			return errors.New("invalid collection shard for trace frame")
		}
		r := item.Run
		if item.Kind != "run" || scalar(r["status"]) != "completed" {
			continue
		}
		repo := strings.ToLower(scalar(r["repository"]))
		if !strings.Contains(repo, "/") {
			repo = strings.ToLower(scalar(r["organization"])) + "/" + repo
		}
		if repo != repository {
			return errors.New("out-of-scope run in trace frame")
		}
		id := scalar(r["run_id"])
		if id == "" {
			id = scalar(r["database_id"])
		}
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return errors.New("invalid trace run identity")
		}
		attempt, err := strconv.ParseInt(scalar(r["run_attempt"]), 10, 64)
		if err != nil || attempt < 1 {
			return errors.New("trace frame requires an exact run attempt")
		}
		completed := scalar(r["completed_at"])
		if completed == "" {
			completed = scalar(r["updated_at"])
		}
		closed, err := time.Parse(time.RFC3339Nano, completed)
		if err != nil {
			return errors.New("trace frame requires a recorded completed-run timestamp")
		}
		directory := "run-" + id
		if location, present := r["logs_path"]; present {
			value, ok := location.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return errors.New("invalid trace source directory")
			}
			directory = value
			if filepath.IsAbs(directory) {
				directory, err = filepath.Rel(logs, directory)
				if err != nil {
					return err
				}
			}
		}
		directory = filepath.Clean(directory)
		if directory == ".." || strings.HasPrefix(directory, ".."+string(filepath.Separator)) {
			directory = ""
		}
		key := id + ":" + strconv.FormatInt(attempt, 10)
		value := candidate{id, directory, closed.UTC().Format(time.DateOnly), attempt}
		if old, ok := out[key]; ok && old != value {
			if old.directory != "" && value.directory == "" {
				continue
			}
			if old.directory == value.directory && old.day == value.day {
				continue
			}
			if old.directory != "" && value.directory != "" {
				return errors.New("ambiguous run attempt in trace frame")
			}
		}
		out[key] = value
		if len(out) > limit {
			return ErrCapacity
		}
	}
	return scanner.Err()
}

func artifactPath(root *os.Root, directory string) (string, map[string]any, string, error) {
	if directory == "" {
		return "", nil, "artifact-directory-gap", nil
	}
	entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(directory))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, "artifact-unavailable", nil
	}
	if err != nil {
		return "", nil, "", err
	}
	var paths []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return "", nil, "artifact-symlink", nil
		}
		if !entry.IsDir() && entry.Name() == "aw_session.jsonl" {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		} else if entry.IsDir() && (entry.Name() == "usage" || strings.HasSuffix(entry.Name(), "-usage")) {
			candidate := filepath.Join(directory, entry.Name(), "aw_session.jsonl")
			if _, err := root.Stat(candidate); err == nil {
				paths = append(paths, candidate)
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", nil, "", err
			}
		}
	}
	if len(paths) != 1 {
		return "", nil, "artifact-unavailable-or-ambiguous", nil
	}
	var info map[string]any
	for _, location := range []string{filepath.Join(filepath.Dir(paths[0]), "aw_info.json"), filepath.Join(directory, "aw_info.json")} {
		content, err := sourceFile(root, location, 1<<20)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", nil, "", err
		}
		if err := decode(content, &info); err != nil {
			//nolint:nilerr // Invalid evidence is a durable source gap, not a service failure.
			return "", nil, "artifact-identity-gap", nil
		}

		return paths[0], info, "", nil
	}
	return "", nil, "artifact-identity-gap", nil
}
