package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeDownloadExplicitExternalOutputParity(t *testing.T) {
	for _, mode := range []string{"absolute", "relative parent", "symbolic output root"} {
		t.Run(mode, func(t *testing.T) {
			nativeManagementDirectory(t)
			base := mustNativeWorkingDirectory(t)
			actual := filepath.Join(base, "external", "snapshot")
			writeTestFile(t, filepath.Join(actual, "operator-notes.txt"), "preserve unrelated output")
			if err := os.Mkdir("checkout", 0o755); err != nil {
				t.Fatal(err)
			}
			output := actual
			switch mode {
			case "relative parent":
				output = filepath.Join("..", "external", "snapshot")
			case "symbolic output root":
				if err := os.Symlink(actual, "output-link"); err != nil {
					t.Fatal(err)
				}
				output = filepath.Join("..", "output-link")
			}
			payloads, digest := nativeDownloadFixture(t, true)
			server := nativeDownloadServer(t, payloads)
			t.Chdir("checkout")
			cmd, stdout := nativeManagementCommand()
			for name, value := range map[string]string{
				"url": server.URL + "/data/payload-hashes.json", "output": output, "manifest-sha256": digest,
			} {
				cmd.Flags().String(name, "", "")
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatal(err)
				}
			}
			if err := nativeDownload(cmd, nil); err != nil {
				t.Fatalf("explicit output outside checkout was rejected: %v", err)
			}
			var result map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			resolved, err := filepath.Abs(output)
			if err != nil {
				t.Fatal(err)
			}
			if result["manifest"] != filepath.Join(resolved, "payload-hashes.json") {
				t.Fatalf("resolved output = %#v", result)
			}
			for _, name := range []string{"inventory-sources.json", "gh-aw-logs-runs/one.jsonl", "gh-aw-logs-records/one.jsonl"} {
				content, err := os.ReadFile(filepath.Join(actual, filepath.FromSlash(name)))
				if err != nil || string(content) != payloads[name] {
					t.Fatalf("external payload %s = %q, %v", name, content, err)
				}
			}
			notes, err := os.ReadFile(filepath.Join(actual, "operator-notes.txt"))
			if err != nil || string(notes) != "preserve unrelated output" {
				t.Fatal("download changed unrelated output")
			}
			entries, err := os.ReadDir(".")
			if err != nil || len(entries) != 0 {
				t.Fatal("external download wrote into the checkout")
			}
		})
	}
}

func TestNativeDownloadExternalOutputRejectsDangerousManifestEntries(t *testing.T) {
	for _, malicious := range []string{
		"../escape.jsonl", "/absolute/escape.jsonl", "gh-aw-logs-runs/../../escape.jsonl",
		"gh-aw-logs-runs/..\\escape.jsonl", "gh-aw-logs-records/%2e%2e%2fescape.jsonl",
	} {
		t.Run(malicious, func(t *testing.T) {
			nativeManagementDirectory(t)
			base := mustNativeWorkingDirectory(t)
			output := filepath.Join(base, "external")
			writeTestFile(t, filepath.Join(output, "inventory-sources.json"), "previous snapshot")
			payloads, _ := nativeDownloadFixture(t, true)
			var manifest map[string]string
			if err := json.Unmarshal([]byte(payloads["payload-hashes.json"]), &manifest); err != nil {
				t.Fatal(err)
			}
			manifest[malicious] = nativeDownloadDigest("untrusted")
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			payloads["payload-hashes.json"] = string(encoded)
			server := nativeDownloadServer(t, payloads)
			if err := os.Mkdir("checkout", 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir("checkout")
			cmd, _ := nativeManagementCommand()
			cmd.Flags().String("url", server.URL+"/data/payload-hashes.json", "")
			cmd.Flags().String("output", output, "")
			if err := nativeDownload(cmd, nil); err == nil || !strings.Contains(err.Error(), "unsafe path") {
				t.Fatalf("dangerous external manifest error = %v", err)
			}
			content, err := os.ReadFile(filepath.Join(output, "inventory-sources.json"))
			if err != nil || string(content) != "previous snapshot" {
				t.Fatal("dangerous manifest replaced the previous snapshot")
			}
			if _, err := os.Stat(filepath.Join(base, "escape.jsonl")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("manifest entry escaped the explicit output root")
			}
		})
	}
}

func TestNativeDownloadRootedPublicationRejectsEscapes(t *testing.T) {
	nativeManagementDirectory(t)
	staging := ".deployed-dashboard-boundary"
	writeTestFile(t, filepath.Join(staging, "inventory-sources.json"), "verified")
	root := mustNativeWorkingDirectory(t)
	if err := nativePublishDownload(filepath.Join(root, staging), root, []string{"../escape.jsonl"}); err == nil {
		t.Fatal("publication accepted a name outside its output root")
	}
	if err := nativePublishDownload(filepath.Dir(root), root, []string{"inventory-sources.json"}); err == nil {
		t.Fatal("publication accepted staging outside its output root")
	}
	content, err := os.ReadFile(filepath.Join(staging, "inventory-sources.json"))
	if err != nil || string(content) != "verified" {
		t.Fatal("rejected publication changed its staged payload")
	}
}
