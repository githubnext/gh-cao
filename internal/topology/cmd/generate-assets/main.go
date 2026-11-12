// Command generate-assets snapshots the authoritative Azure sources for offline native CLI use.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
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
