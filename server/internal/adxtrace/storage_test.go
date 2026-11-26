package adxtrace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func openLocalObjects(t *testing.T, dir string) localObjects {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return localObjects{root}
}

func TestLocalObjectsListReportsPrivateRegularFiles(t *testing.T) {
	dir := t.TempDir()
	objects := openLocalObjects(t, dir)
	if got, err := objects.List(); err != nil || len(got) != 0 {
		t.Fatalf("List on empty directory = %v, %v, want no objects", got, err)
	}
	if err := objects.Write("a.trace", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := objects.Write("b.dataset", []byte("12345")); err != nil {
		t.Fatal(err)
	}
	got, err := objects.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []ObjectInfo{{Key: "a.trace", Bytes: 3}, {Key: "b.dataset", Bytes: 5}}
	slices.SortFunc(got, func(a, b ObjectInfo) int {
		if a.Key < b.Key {
			return -1
		}
		return 1
	})
	if !slices.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}

func TestLocalObjectsListRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"group readable file", func(t *testing.T, dir string) {
			path := filepath.Join(dir, "shared.trace")
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o640); err != nil { // #nosec G302 -- Test rejection of group-readable files in a private temporary directory.
				t.Fatal(err)
			}
		}},
		{"world readable file", func(t *testing.T, dir string) {
			path := filepath.Join(dir, "public.trace")
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o604); err != nil { // #nosec G302 -- Test rejection of world-readable files in a private temporary directory.
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, dir string) {
			if err := os.Symlink("target", filepath.Join(dir, "link.trace")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			objects := openLocalObjects(t, dir)
			if err := objects.Write("ok.trace", []byte("x")); err != nil {
				t.Fatal(err)
			}
			test.setup(t, dir)
			if got, err := objects.List(); err == nil {
				t.Fatalf("List = %v, want rejection of unsafe entry", got)
			}
		})
	}
}

func TestLocalObjectsListFailsOnClosedRoot(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	objects := localObjects{root}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := objects.List(); err == nil {
		t.Fatalf("List on closed root = %v, want error", got)
	}
}

func TestPrivateObjectInfoDescribesEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.trace"), []byte("four"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir = %v, %v", entries, err)
	}
	got, err := privateObjectInfo(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := (ObjectInfo{Key: "one.trace", Bytes: 4}); got != want {
		t.Fatalf("privateObjectInfo = %v, want %v", got, want)
	}
	if err := os.Chmod(filepath.Join(dir, "one.trace"), 0o666); err != nil { // #nosec G302 -- Test rejection of world-writable files in a private temporary directory.
		t.Fatal(err)
	}
	fresh, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privateObjectInfo(fresh[0]); err == nil {
		t.Fatal("privateObjectInfo accepted a world-writable file")
	}
}
