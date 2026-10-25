package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func TestADXSetupSettingsMatchRuntime(t *testing.T) {
	// The extension and server are separate modules; exercise the actual CLI
	// output through the runtime parsers instead of duplicating their contract.
	binary := filepath.Join(t.TempDir(), "gh-cao")
	build := exec.CommandContext(t.Context(), "go", "-C", "../../..", "build", "-o", binary, "./cmd/gh-cao") // #nosec G204 -- Fixed source package and test-owned output path.
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build extension: %v\n%s", err, output)
	}
	for _, approval := range []string{"--data-approved", "--non-personal-data"} {
		t.Run(approval, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), binary, "setup", "adx", // #nosec G204 -- Execute only the extension just built in the private test directory.
				"--endpoint", "https://synthetic.eastus.kusto.windows.net/",
				"--database", "Synthetic",
				"--tenant-id", "11111111-1111-1111-1111-111111111111",
				"--client-id", "22222222-2222-2222-2222-222222222222",
				"--repository-id", "repo-b", "--repository-id", "repo-a",
				approval, "--single-replica", "--interval", "1h",
				"--max-storage-bytes", "1099511627776", "--dry-run")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("setup adx: %v\n%s", err, &stderr)
			}
			var settings map[string]string
			if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
				t.Fatal(err)
			}
			if _, exists := settings["CAO_ADX_CLIENT_SECRET"]; exists {
				t.Fatal("setup must omit the deployment-injected secret")
			}
			get := func(key string) string { return settings[key] }
			if _, err := adxingest.FromEnv(get); err == nil {
				t.Fatal("settings without separately injected secret must fail closed")
			}
			settings["CAO_ADX_CLIENT_SECRET"] = "synthetic-test-only"
			config, err := adxingest.FromEnv(get)
			if err != nil {
				t.Fatalf("setup settings rejected by snapshot runtime: %v", err)
			}
			if !config.Enabled || !config.SingleReplica || config.Interval != time.Hour ||
				config.MaxStorageBytes != 1<<40 || len(config.RepositoryIDs) != 2 ||
				config.RepositoryIDs[0] != "repo-a" {
				t.Fatalf("snapshot settings did not round trip: %+v", config)
			}
			traceConfig, err := adxtrace.FromEnv(get)
			if err != nil || traceConfig.Enabled || traceConfig.MiningEnabled {
				t.Fatalf("setup activated trace work: enabled=%t, mining=%t, error=%v",
					traceConfig.Enabled, traceConfig.MiningEnabled, err)
			}
		})
	}
}
