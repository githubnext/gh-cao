package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeStandaloneBinary(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "gh-cao")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/gh-cao")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}
	working := t.TempDir()
	inventory := filepath.Join(t.TempDir(), "inventory.json")
	writeTestFile(t, inventory, `{"campaigns":{"rows":[{"campaign":"dependabot","observed-at":"2026-01-01T00:00:00.000Z"}]}}`)
	for _, args := range [][]string{
		{"version"}, {"pages", "--json"}, {"queries", "--json"}, {"query-info", "campaign-runs", "--json"},
		{"prompt", "campaign-runs", "--param", "campaign=dependabot"},
		{"setup", "auth", "hosted-cli", "--repo", "acme/control", "--target-repository", "acme/target", "--dry-run"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command := exec.Command(binary, args...)
			command.Dir = working
			command.Env = append(os.Environ(), "PATH=", "CAO_RUNTIME_ROOT=/not-an-installed-runtime")
			command.Stdin = strings.NewReader(`{"name":"empty-runs","from":"runs","limit":3}`)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("standalone execution: %v\n%s", err, output)
			}
			if args[0] != "version" && args[0] != "prompt" && !json.Valid(output) {
				t.Fatalf("command did not return JSON: %s", output)
			}
			if strings.Contains(string(output), `"availability": "unavailable"`) {
				t.Fatalf("standalone query unavailable: %s", output)
			}
		})
	}
	validation := exec.Command(binary, "validate")
	validation.Dir = working
	validation.Env = append(os.Environ(), "PATH=", "CAO_RUNTIME_ROOT=/not-an-installed-runtime")
	output, err := validation.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(output), `unknown command "validate"`) {
		t.Fatalf("standalone binary accepted Actions-only validation: %v\n%s", err, output)
	}
	files, err := os.ReadDir(working)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("standalone read-only commands wrote to the working directory")
	}
	t.Run("operator lifecycle without Node", func(t *testing.T) {
		testNativeStandaloneLifecycle(t, binary)
	})
}
