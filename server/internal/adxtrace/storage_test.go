package adxtrace

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func openTestRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, dir
}

func TestLocalObjectsListReportsKeysAndSizes(t *testing.T) {
	root, _ := openTestRoot(t)
	objects := localObjects{root}
	if err := objects.Write("a.trace", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := objects.Write("b.dataset", []byte("hello world")); err != nil {
		t.Fatal(err)
	}
	entries, err := objects.List()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, entry := range entries {
		got[entry.Key] = entry.Bytes
	}
	if len(got) != 2 || got["a.trace"] != 3 || got["b.dataset"] != 11 {
		t.Fatalf("List() = %v, want a.trace=3 and b.dataset=11", got)
	}
}

func TestLocalObjectsListRejectsNonPrivateEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{name: "group readable file", setup: func(t *testing.T, dir string) {
			path := filepath.Join(dir, "shared.trace")
			if err := os.WriteFile(path, []byte("x"), 0o644); err != nil { // #nosec G306 -- deliberately permissive fixture.
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil { // #nosec G302 -- deliberately permissive fixture.
				t.Fatal(err)
			}
		}},
		{name: "subdirectory", setup: func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symbolic link", setup: func(t *testing.T, dir string) {
			if err := os.Symlink("missing", filepath.Join(dir, "link.trace")); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, dir := openTestRoot(t)
			test.setup(t, dir)
			entries, err := localObjects{root}.List()
			if err == nil {
				t.Fatalf("List() = %v, want rejection of non-private entry", entries)
			}
		})
	}
}

func TestPrivateObjectInfo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "own.trace"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := privateObjectInfo(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Key != "own.trace" || info.Bytes != 5 {
		t.Fatalf("privateObjectInfo() = %+v, want own.trace with 5 bytes", info)
	}
	if err := os.Remove(filepath.Join(dir, "own.trace")); err != nil {
		t.Fatal(err)
	}
	if _, err := privateObjectInfo(entries[0]); err == nil {
		t.Fatal("privateObjectInfo() on a vanished file succeeded, want error")
	}
}

func TestLocalObjectsListEmptyAndSorted(t *testing.T) {
	root, _ := openTestRoot(t)
	objects := localObjects{root}
	if entries, err := objects.List(); err != nil || len(entries) != 0 {
		t.Fatalf("List() on empty root = %v, %v; want no entries", entries, err)
	}
	for _, name := range []string{"c", "a", "b"} {
		if err := objects.Write(name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := listObjectKeys(t, objects); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("keys = %v, want [a b c]", got)
	}
}
