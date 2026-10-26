package sessionarchive

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func privateRead(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, ErrIntegrity
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) > limit {
		return nil, ErrIntegrity
	}
	return content, nil
}

func durableWrite(root *os.Root, name string, content []byte, immutable bool) (err error) {
	if immutable {
		old, err := privateRead(root, name, int64(len(content))+1)
		if err == nil {
			if string(old) == string(content) {
				return nil
			}
			return ErrIntegrity
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	file, err := root.OpenFile(name+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		if err != nil {
			_ = root.Remove(name + ".tmp")
		}
	}()
	if _, err = file.Write(content); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = root.Rename(name+".tmp", name); err != nil {
		return err
	}
	return syncParents(root, filepath.Dir(name))
}

func syncParents(root *os.Root, path string) error {
	for {
		if err := syncDirectory(root, path); err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		path = filepath.Dir(path)
	}
}

func syncDirectory(root *os.Root, path string) error {
	directory, err := root.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func archivePath(path string) bool {
	return !filepath.IsAbs(path) && filepath.IsLocal(path) &&
		(strings.HasPrefix(path, "data/") || strings.HasPrefix(path, "staging/") || strings.HasPrefix(path, "generations/"))
}

func diskFiles(root *os.Root, limit int) (map[string]int64, error) {
	files := map[string]int64{}
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm()&0077 != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrIntegrity
		}
		if entry.IsDir() {
			return nil
		}
		if path != "owner.lock" && path != "state.json" && path != "state.json.tmp" && !archivePath(path) {
			return ErrIntegrity
		}
		files[path] = info.Size()
		if len(files) > limit {
			return ErrCapacity
		}
		return nil
	})
	return files, err
}
