// Command generate-assets snapshots the authoritative Azure sources for offline native CLI use.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	if err := compileCompact(); err != nil {
		return err
	}
	if err := compileContainerApps(); err != nil {
		return err
	}
	directory := filepath.Join("..", "..", "server", "azure")
	names, err := filepath.Glob(filepath.Join(directory, "*.bicep"))
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no canonical Azure Bicep sources found")
	}
	names = append(names, filepath.Join(directory, "main.json"))
	destination := filepath.Join("assets", "azure-baseline.generated")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	generated := map[string]bool{}
	for _, name := range names {
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		base := filepath.Base(name)
		if err := os.WriteFile(filepath.Join(destination, base), content, 0o644); err != nil {
			return err
		}
		generated[base] = true
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !generated[entry.Name()] {
			if entry.IsDir() {
				return fmt.Errorf("unexpected directory in generated baseline: %s", entry.Name())
			}
			if err := os.Remove(filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileCompact() error {
	return compileContract("assets/compact/main.generated.json", map[string]string{
		"main.bicep":              "assets/compact/main.bicep",
		"postgres.bicep":          "assets/compact/postgres.bicep",
		"vm-host.bicep":           "assets/azure/host.bicep",
		"host-init.yaml":          "assets/azure/host-init.yaml",
		"host-init-postgres.yaml": "assets/azure/host-init-postgres.yaml",
	})
}

func compileContainerApps() error {
	return compileContract("assets/container-apps/main.generated.json", map[string]string{
		"main.bicep":         "assets/container-apps/main.bicep",
		"postgres.bicep":     "assets/compact/postgres.bicep",
		"provisioning.bicep": "../../server/azure/provisioning.bicep",
		"ingestion.bicep":    "../../server/azure/ingestion.bicep",
		"worker-roles.bicep": "../../server/azure/worker-roles.bicep",
	})
}

func compileContract(destination string, sources map[string]string) (err error) {
	stage, err := os.MkdirTemp("", "cao-topology-contract-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	for target, source := range sources {
		content, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, target), content, 0o600); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "bicep", "build", filepath.Join(stage, "main.bicep"), "--no-restore", "--stdout")
	command.Stderr = os.Stderr
	content, err := command.Output()
	if err != nil {
		return fmt.Errorf("compile %s Azure parameter contract with official Bicep CLI: %w", destination, err)
	}
	if !json.Valid(content) {
		return fmt.Errorf("bicep compiler returned invalid ARM JSON")
	}
	return os.WriteFile(destination, content, 0o644)
}
