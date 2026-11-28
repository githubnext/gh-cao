package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openPagesRoot(t *testing.T, directory string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestValidatePagesSite(t *testing.T) {
	const marker = ".cao-pages-demo.json"
	tests := []struct {
		name    string
		mutate  func(t *testing.T, directory string)
		wantErr string
	}{
		{name: "synthetic demo", mutate: func(*testing.T, string) {}},
		{
			name: "missing index",
			mutate: func(t *testing.T, directory string) {
				if err := os.Remove(filepath.Join(directory, "index.html")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "readable built index.html",
		},
		{
			name: "empty index",
			mutate: func(t *testing.T, directory string) {
				if err := os.WriteFile(filepath.Join(directory, "index.html"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "non-empty regular file",
		},
		{
			name: "index is a directory",
			mutate: func(t *testing.T, directory string) {
				path := filepath.Join(directory, "index.html")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "non-empty regular file",
		},
		{
			name: "missing marker",
			mutate: func(t *testing.T, directory string) {
				if err := os.Remove(filepath.Join(directory, marker)); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "synthetic demo",
		},
		{
			name: "marker is not synthetic",
			mutate: func(t *testing.T, directory string) {
				if err := os.WriteFile(filepath.Join(directory, marker), []byte(`{"tool":"cao-pages-demo","synthetic":false}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "synthetic demo",
		},
		{
			name: "marker has another tool",
			mutate: func(t *testing.T, directory string) {
				if err := os.WriteFile(filepath.Join(directory, marker), []byte(`{"tool":"other","synthetic":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "synthetic demo",
		},
		{
			name: "marker is invalid JSON",
			mutate: func(t *testing.T, directory string) {
				if err := os.WriteFile(filepath.Join(directory, marker), []byte(`{`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "synthetic demo",
		},
		{
			name: "marker is oversized",
			mutate: func(t *testing.T, directory string) {
				padded := `{"tool":"cao-pages-demo","synthetic":true,"pad":"` + strings.Repeat("a", 4096) + `"}`
				if err := os.WriteFile(filepath.Join(directory, marker), []byte(padded), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "synthetic demo",
		},
		{
			name: "operational export",
			mutate: func(t *testing.T, directory string) {
				if err := os.WriteFile(filepath.Join(directory, "agent-summary.json"), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "operational export agent-summary.json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory, _ := pagesFixture(t)
			test.mutate(t, directory)
			err := validatePagesSite(openPagesRoot(t, directory))
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("rejected valid site: %v", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}
