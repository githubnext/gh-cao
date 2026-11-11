//go:build dashboard_embed

package dashboardassets

import (
	"bytes"
	"io/fs"
	"os"
	"testing"
)

func TestEmbeddedDashboardIncludesEveryStagedFile(t *testing.T) {
	site, err := Site()
	if err != nil {
		t.Fatal(err)
	}
	staged := os.DirFS("resources/site")
	files := 0
	if err := fs.WalkDir(staged, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files++
		t.Run(name, func(t *testing.T) {
			expected, err := fs.ReadFile(staged, name)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := fs.ReadFile(site, name)
			if err != nil {
				t.Fatalf("dashboard file was not embedded: %v", err)
			}
			if !bytes.Equal(actual, expected) {
				t.Fatal("embedded dashboard file differs from its staged contents")
			}
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no staged dashboard files were checked")
	}
}
