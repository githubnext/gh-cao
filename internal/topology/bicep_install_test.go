package topology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func compilerRelease(content []byte) bicepRelease {
	digest := sha256.Sum256(content)
	return bicepRelease{"bicep", int64(len(content)), hex.EncodeToString(digest[:])}
}

func TestBicepCompilerCacheWithoutPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	content := []byte("verified compiler")
	release := compilerRelease(content)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(content)
	}))
	defer server.Close()
	directory := filepath.Join(t.TempDir(), "cache")
	filename, err := cacheBicepCompiler(context.Background(), directory, release, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(filename) {
		t.Fatalf("compiler path is not absolute: %s", filename)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("compiler is not private/executable: %v", info.Mode())
	}
	server.Close()
	cached, err := cacheBicepCompiler(context.Background(), directory, release, server.URL, server.Client())
	if err != nil || cached != filename || requests.Load() != 1 {
		t.Fatalf("verified cache was not reused offline: %s, %v, %d requests", cached, err, requests.Load())
	}
	if err := os.WriteFile(filename, []byte(strings.Repeat("x", len(content))), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheBicepCompiler(context.Background(), directory, release, server.URL, server.Client()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered cached compiler was accepted: %v", err)
	}
}

func TestBicepCompilerRejectsInvalidDownloads(t *testing.T) {
	content := []byte("verified compiler")
	release := compilerRelease(content)
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"http-error", http.StatusServiceUnavailable, ""},
		{"truncated", http.StatusOK, "short"},
		{"wrong-digest", http.StatusOK, strings.Repeat("x", len(content))},
		{"oversized", http.StatusOK, strings.Repeat("x", len(content)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			directory := filepath.Join(t.TempDir(), "cache")
			if _, err := cacheBicepCompiler(context.Background(), directory, release, server.URL, server.Client()); err == nil {
				t.Fatal("invalid compiler download was accepted")
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed download published files or left staging files: %v, %v", entries, err)
			}
		})
	}
}

func TestBicepCompilerConcurrentPublication(t *testing.T) {
	content := []byte("verified compiler")
	release := compilerRelease(content)
	var downloads atomic.Int64
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if downloads.Add(1) == 2 {
			close(ready)
		}
		select {
		case <-ready:
			_, _ = writer.Write(content)
		case <-request.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	directory := filepath.Join(t.TempDir(), "cache")
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := cacheBicepCompiler(ctx, directory, release, server.URL, server.Client())
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyBicepCompiler(context.Background(), filepath.Join(directory, release.name), release); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("concurrent acquisition left staging files: %v, %v", entries, err)
	}
}

func TestBicepCompilerRejectsSymlinkAndCancellation(t *testing.T) {
	content := []byte("verified compiler")
	release := compilerRelease(content)
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, content, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, release.name)); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheBicepCompiler(context.Background(), directory, release, "", http.DefaultClient); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("cached compiler symlink was accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		cancel()
		_, _ = writer.Write(content)
	}))
	defer server.Close()
	directory = filepath.Join(t.TempDir(), "cache")
	if _, err := cacheBicepCompiler(ctx, directory, release, server.URL, server.Client()); !errors.Is(err, context.Canceled) {
		t.Fatalf("compiler acquisition lost cancellation: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled download published files or left staging files: %v, %v", entries, err)
	}
}
