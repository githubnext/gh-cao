package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeDownloadDigest(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func nativeDownloadFixture(t *testing.T, normalized bool) (map[string]string, string) {
	t.Helper()
	payloads := map[string]string{"inventory-sources.json": `{"schemaVersion":1}`}
	if normalized {
		payloads["gh-aw-logs-runs/one.jsonl"] = "{\"kind\":\"metadata\",\"phase\":\"runs\",\"records\":0}\n"
		payloads["gh-aw-logs-records/one.jsonl"] = "{\"kind\":\"metadata\",\"phase\":\"records\",\"records\":0}\n"
	} else {
		payloads["gh-aw-logs-shards/one.jsonl"] = "{\"kind\":\"run\"}\n"
	}
	hashes := map[string]string{}
	for name, content := range payloads {
		hashes[name] = nativeDownloadDigest(content)
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

func TestNativeDownloadRequiresExplicitSource(t *testing.T) {
	nativeManagementDirectory(t)
	t.Setenv("DASHBOARD_DATA_URL", "")
	cmd, _ := nativeManagementCommand()
	if err := nativeDownload(cmd, nil); err == nil || !strings.Contains(err.Error(), "gh run download") {
		t.Fatalf("missing source must explain authenticated artifact acquisition: %v", err)
	}
	if _, err := os.Stat(".cao"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unconfigured acquisition created a cache")
	}
	for _, source := range []string{"https://snapshot.example/", "https://snapshot.example/cao/"} {
		if _, err := nativeDownloadSnapshot(t.Context(), source, ".cao", "", nil); err == nil {
			t.Fatal("directory source triggered implicit discovery")
		}
	}
}

func TestNativeDownloadPublishesVerifiedCanonicalShards(t *testing.T) {
	nativeManagementDirectory(t)
	payloads, digest := nativeDownloadFixture(t, true)
	server := nativeDownloadServer(t, payloads)
	result, err := nativeDownloadSnapshot(t.Context(), server.URL+"/data/payload-hashes.json", "snapshot", digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"inventory-sources.json", "gh-aw-logs-runs/one.jsonl", "gh-aw-logs-records/one.jsonl"} {
		content, err := os.ReadFile(filepath.Join("snapshot", filepath.FromSlash(name)))
		if err != nil || string(content) != payloads[name] {
			t.Fatalf("verified payload %s: %v", name, err)
		}
	}
	if result["manifest"] != filepath.Join(mustNativeWorkingDirectory(t), "snapshot", "payload-hashes.json") {
		t.Fatalf("wrong manifest location: %v", result)
	}
}

func TestNativeDownloadRejectsRawAndSyntheticInputs(t *testing.T) {
	for _, input := range []string{"raw", "synthetic"} {
		t.Run(input, func(t *testing.T) {
			nativeManagementDirectory(t)
			payloads, _ := nativeDownloadFixture(t, false)
			if input == "synthetic" {
				payloads["payload-hashes.json"] = `{"synthetic":true}`
			}
			server := nativeDownloadServer(t, payloads)
			if _, err := nativeDownloadSnapshot(t.Context(), server.URL+"/data/payload-hashes.json", "snapshot", "", nil); err == nil {
				t.Fatal("unsupported operational input was admitted")
			}
		})
	}
}

func TestNativeDownloadHashFailurePreservesPreviousEvidence(t *testing.T) {
	nativeManagementDirectory(t)
	payloads, _ := nativeDownloadFixture(t, true)
	payloads["gh-aw-logs-runs/one.jsonl"] = "corrupt"
	server := nativeDownloadServer(t, payloads)
	writeTestFile(t, "snapshot/inventory-sources.json", "previous")
	writeTestFile(t, "snapshot/payload-hashes.json", "previous manifest")
	if _, err := nativeDownloadSnapshot(context.Background(), server.URL+"/data/payload-hashes.json", "snapshot", "", nil); err == nil {
		t.Fatal("corrupt evidence was published")
	}
	for name, expected := range map[string]string{"inventory-sources.json": "previous", "payload-hashes.json": "previous manifest"} {
		content, err := os.ReadFile(filepath.Join("snapshot", name))
		if err != nil || string(content) != expected {
			t.Fatalf("failed acquisition changed %s", name)
		}
	}
}

func mustNativeWorkingDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return directory
}
