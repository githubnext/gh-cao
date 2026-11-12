package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func nativeDownloadDigest(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func nativeDownloadFixture(t *testing.T, normalized bool) (map[string]string, string) {
	t.Helper()
	payloads := map[string]string{
		"gh-aw-logs.sqlite":      "SQLite format 3\x00verified snapshot",
		"inventory-sources.json": `{"schemaVersion":1}`,
	}
	if normalized {
		payloads["gh-aw-logs-runs/one.jsonl"] = "{\"type\":\"run\"}\n"
		payloads["gh-aw-logs-records/one.jsonl"] = "{\"type\":\"record\"}\n"
		payloads["gh-aw-logs-records/empty.jsonl"] = ""
	} else {
		payloads["gh-aw-logs-shards/one.jsonl"] = "{\"type\":\"raw\"}\n"
	}
	hashes := map[string]string{"gh-aw-logs.sqlite": nativeDownloadDigest(payloads["gh-aw-logs.sqlite"])}
	for name, content := range payloads {
		if strings.HasSuffix(name, ".jsonl") {
			hashes[name] = nativeDownloadDigest(content)
		}
	}
	content, err := json.Marshal(hashes)
	if err != nil {
		t.Fatal(err)
	}
	payloads["payload-hashes.json"] = string(content)
	return payloads, nativeDownloadDigest(string(content))
}

func nativeDownloadServer(t *testing.T, payloads map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		content, found := payloads[strings.TrimPrefix(request.URL.Path, "/data/")]
		if !found {
			http.NotFound(writer, request)
			return
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

type nativeDownloadTestTransport func(*http.Request) (*http.Response, error)

func (f nativeDownloadTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNativeDownloadRequiresExplicitSource(t *testing.T) {
	nativeManagementDirectory(t)
	t.Setenv("DASHBOARD_DATA_URL", "")
	previous := http.DefaultTransport
	http.DefaultTransport = nativeDownloadTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("unconfigured sources must never use the network")
		return nil, errors.New("unexpected network request")
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, cached := range []bool{false, true} {
		if cached {
			writeTestFile(t, ".cao/gh-aw-logs.sqlite", "existing cache")
		}
		cmd, _ := nativeManagementCommand()
		if err := nativeDownload(cmd, nil); err == nil ||
			!strings.Contains(err.Error(), "gh run download RUN_ID --repo OWNER/REPO --name cao-activity-index --dir .cao") {
			t.Fatalf("missing source must explain artifact acquisition: %v", err)
		}
		if !cached {
			if _, err := os.Stat(".cao"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("missing source created a cache")
			}
		}
	}
	for _, source := range []string{"https://snapshot.example/", "https://snapshot.example/cao/", "https://snapshot.example/index.html"} {
		if _, err := nativeDownloadSnapshot(t.Context(), source, ".cao", "", nil); err == nil || !strings.Contains(err.Error(), "must identify payload-hashes.json") {
			t.Fatalf("directory must not trigger discovery: %v", err)
		}
	}
	content, err := os.ReadFile(".cao/gh-aw-logs.sqlite")
	if err != nil || string(content) != "existing cache" {
		t.Fatalf("missing source changed the cache: %q, %v", content, err)
	}
}

func TestNativeDownloadRejectsSyntheticAndSQLiteFreeManifest(t *testing.T) {
	for _, manifest := range []string{
		`{"synthetic":true,"gh-aw-logs.sqlite":"` + strings.Repeat("a", 64) + `"}`,
		`{"gh-aw-logs-runs/demo.jsonl":"` + strings.Repeat("a", 64) + `"}`,
	} {
		t.Run(manifest, func(t *testing.T) {
			output := t.TempDir()
			writeTestFile(t, filepath.Join(output, "gh-aw-logs.sqlite"), "existing cache")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/payload-hashes.json" {
					t.Errorf("unexpected payload request: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(manifest))
			}))
			defer server.Close()
			_, err := nativeDownloadSnapshot(t.Context(), server.URL+"/payload-hashes.json", output, "", server.Client())
			if err == nil || (!strings.Contains(err.Error(), "Synthetic Pages demo") && !strings.Contains(err.Error(), "no valid SQLite checksum")) {
				t.Fatalf("non-operational manifest accepted: %v", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("requested payloads for a rejected manifest: %d", requests.Load())
			}
			content, err := os.ReadFile(filepath.Join(output, "gh-aw-logs.sqlite"))
			if err != nil || string(content) != "existing cache" {
				t.Fatal("rejected manifest changed cache")
			}
		})
	}
}

func TestNativeDownloadExplicitEnvironmentSource(t *testing.T) {
	nativeManagementDirectory(t)
	payloads, _ := nativeDownloadFixture(t, true)
	server := nativeDownloadServer(t, payloads)
	t.Setenv("DASHBOARD_DATA_URL", server.URL+"/data/payload-hashes.json")
	cmd, _ := nativeManagementCommand()
	if err := nativeDownload(cmd, nil); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(".cao/gh-aw-logs.sqlite")
	if err != nil || string(content) != payloads["gh-aw-logs.sqlite"] {
		t.Fatalf("explicit environment source not published: %q, %v", content, err)
	}
}

func TestNativeDownloadPublishesOnlyVerifiedSnapshot(t *testing.T) {
	for _, normalized := range []bool{true, false} {
		t.Run(map[bool]string{true: "normalized", false: "raw"}[normalized], func(t *testing.T) {
			nativeManagementDirectory(t)
			payloads, digest := nativeDownloadFixture(t, normalized)
			server := nativeDownloadServer(t, payloads)
			result, err := nativeDownloadSnapshot(context.Background(), server.URL+"/data/payload-hashes.json", "snapshot", digest, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			database, err := os.ReadFile("snapshot/gh-aw-logs.sqlite")
			if err != nil || string(database) != payloads["gh-aw-logs.sqlite"] {
				t.Fatalf("published database = %q, %v", database, err)
			}
			content, err := os.ReadFile("snapshot/payload-hashes.json")
			if err != nil {
				t.Fatal(err)
			}
			var hashes map[string]string
			if err := json.Unmarshal(content, &hashes); err != nil {
				t.Fatal(err)
			}
			if _, exists := hashes["gh-aw-logs-records/empty.jsonl"]; exists {
				t.Fatal("empty shard was retained")
			}
			if result["database"] != filepath.Join(mustNativeWorkingDirectory(t), "snapshot", "gh-aw-logs.sqlite") {
				t.Fatalf("output = %#v", result)
			}
			entries, err := os.ReadDir("snapshot")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".deployed-dashboard-") {
					t.Fatal("download staging was not cleaned")
				}
			}
		})
	}
}

func mustNativeWorkingDirectory(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNativeDownloadHashFailurePreservesPreviousSnapshot(t *testing.T) {
	for _, failed := range []string{"manifest", "database", "shard"} {
		t.Run(failed, func(t *testing.T) {
			nativeManagementDirectory(t)
			payloads, digest := nativeDownloadFixture(t, true)
			switch failed {
			case "manifest":
				digest = strings.Repeat("0", 64)
			case "database":
				payloads["gh-aw-logs.sqlite"] = "corrupted"
			case "shard":
				payloads["gh-aw-logs-runs/one.jsonl"] = "corrupted"
			}
			writeTestFile(t, "snapshot/gh-aw-logs.sqlite", "previous database")
			writeTestFile(t, "snapshot/payload-hashes.json", "previous manifest")
			server := nativeDownloadServer(t, payloads)
			if _, err := nativeDownloadSnapshot(context.Background(), server.URL+"/data/payload-hashes.json", "snapshot", digest, server.Client()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
				t.Fatalf("error = %v", err)
			}
			for name, expected := range map[string]string{"gh-aw-logs.sqlite": "previous database", "payload-hashes.json": "previous manifest"} {
				content, err := os.ReadFile(filepath.Join("snapshot", name))
				if err != nil || string(content) != expected {
					t.Fatalf("unverified download changed %s: %q, %v", name, content, err)
				}
			}
		})
	}
}

func TestNativeDownloadRejectsPathsAndCredentials(t *testing.T) {
	for _, name := range []string{
		"gh-aw-logs-runs/../outside.jsonl", "gh-aw-logs-runs/a/b.jsonl", "gh-aw-logs-runs/a\\b.jsonl",
		"gh-aw-logs-records/%2e%2e.jsonl", "gh-aw-logs-shards/name?secret.jsonl", "gh-aw-logs-runs/a#b.jsonl",
	} {
		if _, _, err := nativeShardName(name); err == nil {
			t.Errorf("accepted unsafe shard %q", name)
		}
	}
	for _, source := range []string{"file:///payload-hashes.json", "https://user:password@example.com/payload-hashes.json", "ftp://example.com/payload-hashes.json"} {
		if _, err := nativeDownloadURL(source); err == nil {
			t.Errorf("accepted unsafe source %q", source)
		}
	}
	nativeManagementDirectory(t)
	payloads, _ := nativeDownloadFixture(t, true)
	var hashes map[string]string
	if err := json.Unmarshal([]byte(payloads["payload-hashes.json"]), &hashes); err != nil {
		t.Fatal(err)
	}
	hashes["gh-aw-logs-runs/../escape.jsonl"] = strings.Repeat("a", 64)
	content, err := json.Marshal(hashes)
	if err != nil {
		t.Fatal(err)
	}
	payloads["payload-hashes.json"] = string(content)
	server := nativeDownloadServer(t, payloads)
	if _, err := nativeDownloadSnapshot(context.Background(), server.URL+"/data/payload-hashes.json", "snapshot", "", server.Client()); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat("snapshot/escape.jsonl"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe payload escaped its directory")
	}
}

func TestNativeDownloadHTTPFailureAndCancellation(t *testing.T) {
	nativeManagementDirectory(t)
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(status) }))
		err := nativeFetchFile(context.Background(), server.Client(), mustNativeDownloadURL(t, server.URL+"/file"), "not-written", false)
		server.Close()
		var failure *nativeDownloadFailure
		if !errors.As(err, &failure) || failure.retry != (status == http.StatusServiceUnavailable) {
			t.Fatalf("HTTP %d error = %#v", status, err)
		}
		if _, err := os.Stat("not-written"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("HTTP failure published a file")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := nativeDownloadSnapshot(ctx, "https://example.invalid/payload-hashes.json", "snapshot", "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled download error = %v", err)
	}
}

func mustNativeDownloadURL(t *testing.T, source string) *url.URL {
	t.Helper()
	value, err := nativeDownloadURL(source)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestNativeDownloadRejectsHTTPSDowngrade(t *testing.T) {
	for _, resource := range []string{"payload-hashes.json", "gh-aw-logs.sqlite"} {
		t.Run(resource, func(t *testing.T) {
			payloads, _ := nativeDownloadFixture(t, true)
			var plaintextRequests atomic.Int64
			plaintext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				plaintextRequests.Add(1)
				_, _ = w.Write([]byte(payloads[strings.TrimPrefix(r.URL.Path, "/data/")]))
			}))
			defer plaintext.Close()
			secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/"+resource) {
					http.Redirect(w, r, plaintext.URL+r.URL.Path, http.StatusFound)
					return
				}
				_, _ = w.Write([]byte(payloads[strings.TrimPrefix(r.URL.Path, "/data/")]))
			}))
			defer secure.Close()
			client := secure.Client()
			// A caller's permissive policy must not override the downgrade guard.
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
			directory := t.TempDir()
			database := filepath.Join(directory, "gh-aw-logs.sqlite")
			writeTestFile(t, database, "previous snapshot")
			if _, err := nativeDownloadSnapshot(t.Context(), secure.URL+"/data/payload-hashes.json", directory, "", client); !errors.Is(err, errNativeDownloadDowngrade) {
				t.Fatalf("expected explicit HTTPS downgrade rejection, got %v", err)
			}
			if plaintextRequests.Load() != 0 {
				t.Fatal("plaintext destination received a request")
			}
			if content, err := os.ReadFile(database); err != nil || string(content) != "previous snapshot" {
				t.Fatalf("downgrade changed previous snapshot: %q, %v", content, err)
			}
		})
	}
}

func TestNativeDownloadPublicationRejectsExternalSymlink(t *testing.T) {
	nativeManagementDirectory(t)
	outside, err := os.MkdirTemp("..", ".native-download-outside-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(outside); err != nil {
			t.Error(err)
		}
	})
	outside, err = filepath.Abs(outside)
	if err != nil {
		t.Fatal(err)
	}
	payloads, digest := nativeDownloadFixture(t, true)
	server := nativeDownloadServer(t, payloads)
	if err := os.Mkdir("snapshot", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, "snapshot/gh-aw-logs-runs"); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeDownloadSnapshot(context.Background(), server.URL+"/data/payload-hashes.json", "snapshot", digest, server.Client()); err == nil {
		t.Fatal("published through external symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("download modified external directory")
	}
}
