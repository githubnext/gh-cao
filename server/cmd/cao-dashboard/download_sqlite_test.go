package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sqlitex"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestSQLiteManifestURL(t *testing.T) {
	for _, input := range []string{
		"https://snapshot.example.org/custom/payload-hashes.json",
		"http://127.0.0.1:8080/payload-hashes.json",
	} {
		t.Run(input, func(t *testing.T) {
			location, err := sqliteManifestURL(input)
			if err != nil {
				t.Fatal(err)
			}
			if location.String() != input {
				t.Fatalf("location = %s, want %s", location, input)
			}
		})
	}
	for _, input := range []string{"", "file:///data", "/relative", "https://user:password@example.org", "https://example.org/#secret", "https://%",
		"https://owner.github.io/project", "https://owner.github.io/project/cao/", "https://dashboard.example.org", "https://dashboard.example.org/project/index.html"} {
		if _, err := sqliteManifestURL(input); err == nil || strings.Contains(err.Error(), input) && input != "" {
			t.Errorf("URL must fail without echoing unsafe input: %q: %v", input, err)
		}
	}
}

func TestDownloadSQLiteDoesNotDiscoverDirectories(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer remote.Close()
	directory := t.TempDir()
	destination := filepath.Join(directory, "snapshot.sqlite")
	for _, source := range []string{"", remote.URL, remote.URL + "/cao/", remote.URL + "/index.html"} {
		if err := downloadSQLite(t.Context(), source, destination); err == nil {
			t.Fatalf("accepted implicit manifest: %q", source)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("directory discovery requested %d URLs", requests.Load())
	}
	if err := downloadSQLite(t.Context(), remote.URL+"/payload-hashes.json", destination); err == nil {
		t.Fatal("accepted missing explicit manifest")
	}
	if requests.Load() != 1 {
		t.Fatalf("missing manifest must not probe alternate locations: %d", requests.Load())
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("invalid source created a snapshot: %v, %v", entries, err)
	}
}

func sqliteDownloadFixture(t *testing.T) []byte {
	t.Helper()
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"repositories": {{"id": "owner/repo", "fullName": "owner/repo"}},
	})
	content, err := os.ReadFile(path) // #nosec G304 -- fixture path created by testing.T.
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func sqliteDownloadManifest(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf(`{"gh-aw-logs.sqlite":%q}`, hex.EncodeToString(sum[:]))
}

func TestDownloadSQLiteResolvesImageAfterManifestRedirect(t *testing.T) {
	content := sqliteDownloadFixture(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /project/payload-hashes.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/canonical/cao/payload-hashes.json", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /canonical/cao/payload-hashes.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, sqliteDownloadManifest(content))
	})
	mux.HandleFunc("GET /canonical/cao/gh-aw-logs.sqlite", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	})
	remote := httptest.NewServer(mux)
	defer remote.Close()
	destination := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if err := downloadSQLite(t.Context(), remote.URL+"/project/payload-hashes.json", destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination) // #nosec G304 -- cache path is within testing.T.TempDir.
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("redirected snapshot differs: %v", err)
	}
}

func TestDownloadSQLiteFromExplicitManifests(t *testing.T) {
	content := sqliteDownloadFixture(t)
	for _, test := range []struct {
		name string
		base string
	}{
		{"nested", "/project/data"},
		{"root", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			mux.HandleFunc("GET "+test.base+"/payload-hashes.json", func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cache-Control") != "no-cache" {
					t.Error("manifest must be revalidated")
				}
				_, _ = fmt.Fprint(w, sqliteDownloadManifest(content))
			})
			mux.HandleFunc("GET "+test.base+"/gh-aw-logs.sqlite", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(content)
			})
			remote := httptest.NewServer(mux)
			defer remote.Close()
			destination := filepath.Join(t.TempDir(), "cache", "snapshot.sqlite")
			if err := downloadSQLite(t.Context(), remote.URL+test.base+"/payload-hashes.json", destination); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(destination) // #nosec G304 -- cache path is within testing.T.TempDir.
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("downloaded snapshot differs: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary downloads were not removed: %v, %v", entries, err)
			}
			store, err := sqlitex.Open(t.Context(), destination)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			state, err := store.State(t.Context())
			if err != nil || state.Counts["$repositories"] != 1 {
				t.Fatalf("snapshot is not queryable: %+v, %v", state, err)
			}
		})
	}
}

func TestDownloadSQLitePreservesCacheOnFailure(t *testing.T) {
	content := sqliteDownloadFixture(t)
	for _, test := range []struct {
		name     string
		manifest string
		image    []byte
		status   int
		want     string
	}{
		{"missing", "", nil, http.StatusNotFound, "HTTP 404"},
		{"forbidden", "", nil, http.StatusForbidden, "HTTP 403"},
		{"invalid-json", "{", nil, 0, "JSON object"},
		{"no-digest", "{}", nil, 0, "SHA-256"},
		{"invalid-digest", `{"gh-aw-logs.sqlite":"invalid"}`, nil, 0, "SHA-256"},
		{"synthetic", `{"synthetic":true,"gh-aw-logs.sqlite":"` + strings.Repeat("a", 64) + `"}`, content, 0, "synthetic Pages demo"},
		{"empty-image", sqliteDownloadManifest(nil), nil, 0, "empty response"},
		{"checksum", sqliteDownloadManifest(content), []byte("different"), 0, "checksum mismatch"},
		{"not-sqlite", sqliteDownloadManifest([]byte("invalid")), []byte("invalid"), 0, "validate downloaded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch {
				case test.status != 0:
					w.WriteHeader(test.status)
				case strings.HasSuffix(r.URL.Path, "/payload-hashes.json"):
					_, _ = fmt.Fprint(w, test.manifest)
				default:
					_, _ = w.Write(test.image)
				}
			}))
			defer remote.Close()
			destination := filepath.Join(t.TempDir(), "snapshot.sqlite")
			if err := os.WriteFile(destination, content, 0o600); err != nil {
				t.Fatal(err)
			}
			err := downloadSQLite(t.Context(), remote.URL+"/payload-hashes.json", destination)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
			if test.name == "synthetic" && requests.Load() != 1 {
				t.Fatalf("synthetic manifest triggered payload acquisition: %d", requests.Load())
			}
			got, err := os.ReadFile(destination) // #nosec G304 -- cache path is within testing.T.TempDir.
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("previous snapshot was changed: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary downloads were not removed: %v, %v", entries, err)
			}
		})
	}
}

func TestDownloadSQLiteRetriesChangingDeployment(t *testing.T) {
	content := sqliteDownloadFixture(t)
	var manifests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/payload-hashes.json") {
			if manifests.Add(1) == 1 {
				_, _ = fmt.Fprint(w, sqliteDownloadManifest([]byte("previous deployment")))
			} else {
				_, _ = fmt.Fprint(w, sqliteDownloadManifest(content))
			}
		} else {
			_, _ = w.Write(content)
		}
	}))
	defer remote.Close()
	destination := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if err := os.WriteFile(destination, []byte("previous cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := downloadSQLite(t.Context(), remote.URL+"/payload-hashes.json", destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination) // #nosec G304 -- cache path is within testing.T.TempDir.
	if err != nil || !bytes.Equal(got, content) || manifests.Load() != 2 {
		t.Fatalf("expected a fresh manifest and atomic replacement: attempts=%d err=%v", manifests.Load(), err)
	}
}

func TestDownloadSQLiteRetriesTransientHTTPFailure(t *testing.T) {
	content := sqliteDownloadFixture(t)
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/payload-hashes.json") {
			_, _ = fmt.Fprint(w, sqliteDownloadManifest(content))
		} else {
			_, _ = w.Write(content)
		}
	}))
	defer remote.Close()
	if err := downloadSQLite(t.Context(), remote.URL+"/payload-hashes.json", filepath.Join(t.TempDir(), "snapshot.sqlite")); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadSQLiteRejectsUnsafeDestinations(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal", "directory", "symlink"} {
		t.Run(suffix, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "snapshot.sqlite")
			switch suffix {
			case "directory":
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing", destination); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(destination+suffix, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := downloadSQLite(t.Context(), "https://snapshot.example.org/payload-hashes.json", destination); err == nil {
				t.Fatal("unsafe destination accepted")
			}
		})
	}
}

func TestFetchSQLitePayloadIsBounded(t *testing.T) {
	for _, knownLength := range []bool{true, false} {
		t.Run(fmt.Sprint(knownLength), func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if !knownLength {
					w.(http.Flusher).Flush()
				}
				_, _ = fmt.Fprint(w, strings.Repeat("x", 20))
			}))
			defer remote.Close()
			location, err := url.Parse(remote.URL)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			_, err = fetchSQLitePayload(t.Context(), remote.Client(), location, "manifest", &output, 10)
			if err == nil || !strings.Contains(err.Error(), "10-byte download limit") || output.Len() > 11 {
				t.Fatalf("unbounded download: size=%d, err=%v", output.Len(), err)
			}
		})
	}
}

func TestDownloadSQLiteCancellation(t *testing.T) {
	started := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer remote.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	destination := filepath.Join(t.TempDir(), "snapshot.sqlite")
	go func() { done <- downloadSQLite(ctx, remote.URL+"/payload-hashes.json", destination) }()
	<-started
	cancel()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancellation was not propagated: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("canceled download published a snapshot: %v", err)
	}
}

func TestParseSQLiteChecksum(t *testing.T) {
	valid := strings.Repeat("AB", 32)
	got, err := parseSQLiteChecksum([]byte(`{"gh-aw-logs.sqlite":"` + valid + `"}`))
	if err != nil || got != strings.ToLower(valid) {
		t.Fatalf("parseSQLiteChecksum() = %q, %v", got, err)
	}
	for name, manifest := range map[string]string{
		"not an object": `[]`,
		"missing":       `{}`,
		"not a string":  `{"gh-aw-logs.sqlite":5}`,
		"not hex":       `{"gh-aw-logs.sqlite":"zz"}`,
		"short":         `{"gh-aw-logs.sqlite":"abcd"}`,
	} {
		if _, err := parseSQLiteChecksum([]byte(manifest)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRetryableSQLiteStatus(t *testing.T) {
	for status, want := range map[int]bool{
		200: false, 404: false, 408: true, 425: true, 429: true, 499: false, 500: true, 503: true,
	} {
		if got := retryableSQLiteStatus(status); got != want {
			t.Errorf("retryableSQLiteStatus(%d) = %t, want %t", status, got, want)
		}
	}
}

func TestCheckSQLiteRedirect(t *testing.T) {
	newRequest := func(raw string) *http.Request {
		location, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: location}
	}
	chain := func(n int, raw string) []*http.Request {
		via := make([]*http.Request, n)
		for i := range via {
			via[i] = newRequest(raw)
		}
		return via
	}
	for _, test := range []struct {
		name    string
		target  string
		via     []*http.Request
		wantErr string
	}{
		{"https to https", "https://b.example/x", chain(1, "https://a.example/"), ""},
		{"http to https", "https://b.example/x", chain(1, "http://a.example/"), ""},
		{"http to http", "http://b.example/x", chain(1, "http://a.example/"), ""},
		{"https downgrade", "http://b.example/x", chain(1, "https://a.example/"), "unsafe SQLite download redirect"},
		{"credentials", "https://user:pass@b.example/x", chain(1, "https://a.example/"), "unsafe SQLite download redirect"},
		{"other scheme", "ftp://b.example/x", chain(1, "http://a.example/"), "unsafe SQLite download redirect"},
		{"nine hops allowed", "https://b.example/x", chain(9, "https://a.example/"), ""},
		{"ten hops rejected", "https://b.example/x", chain(10, "https://a.example/"), "stopped after 10 redirects"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkSQLiteRedirect(newRequest(test.target), test.via)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}
