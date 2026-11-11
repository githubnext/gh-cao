// Command generate-assets snapshots the authoritative Azure sources for offline native CLI use.
package main

import (
	"bytes"
	"compress/gzip"
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
	directory := filepath.Join("..", "..", "server", "azure")
	names, err := filepath.Glob(filepath.Join(directory, "*.bicep"))
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no canonical Azure Bicep sources found")
	}
	names = append(names, filepath.Join(directory, "main.json"))
	files := map[string][]byte{}
	for _, name := range names {
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files[filepath.Base(name)] = content
	}
	content, err := json.Marshal(files)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return os.WriteFile("azure-baseline.generated.json.gz", compressed.Bytes(), 0o644)
}

func compileCompact() (err error) {
	stage, err := os.MkdirTemp("", "cao-compact-contract-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	for target, source := range map[string]string{
		"main.bicep":              "assets/compact/main.bicep",
		"postgres.bicep":          "assets/compact/postgres.bicep",
		"vm-host.bicep":           "assets/azure/host.bicep",
		"host-init.yaml":          "assets/azure/host-init.yaml",
		"host-init-postgres.yaml": "assets/azure/host-init-postgres.yaml",
	} {
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
		return fmt.Errorf("compile compact Azure parameter contract with official Bicep CLI: %w", err)
	}
	if !json.Valid(content) {
		return fmt.Errorf("Bicep compiler returned invalid ARM JSON")
	}
	return os.WriteFile("assets/compact/main.generated.json", content, 0o644)
}
