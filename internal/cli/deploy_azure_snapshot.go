package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func copyDeployAzureSnapshot(source, destination string) error {
	root, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve evidence snapshot: %w", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "payload-hashes.json"))
	if err != nil {
		return fmt.Errorf("snapshot requires payload-hashes.json: %w", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(content, &manifest); err != nil {
		return fmt.Errorf("parse snapshot manifest: %w", err)
	}
	if len(manifest) == 0 {
		return errors.New("snapshot manifest is empty; use --empty-evidence for an explicitly empty deployment")
	}
	runShards := 0
	for name, expected := range manifest {
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") || filepath.ToSlash(filepath.Clean(name)) != name ||
			!regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(expected) {
			return fmt.Errorf("unsafe snapshot manifest path or digest: %q", name)
		}
		if !strings.HasPrefix(name, "gh-aw-logs-runs/") && !strings.HasPrefix(name, "gh-aw-logs-records/") && name != "inventory-sources.json" {
			return fmt.Errorf("snapshot manifest file is not allowlisted: %q", name)
		}
		if strings.HasPrefix(name, "gh-aw-logs-runs/") {
			runShards++
		}
		input := filepath.Join(root, filepath.FromSlash(name))
		output := filepath.Join(destination, filepath.FromSlash(name))
		if err := copyDeployAzureFile(input, output); err != nil {
			return fmt.Errorf("copy snapshot payload %s: %w", name, err)
		}
		payload, err := os.ReadFile(output)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
			return fmt.Errorf("snapshot SHA-256 mismatch: %s", name)
		}
	}
	if runShards == 0 {
		return errors.New("snapshot has no compacted run-information shards")
	}
	inventory := filepath.Join(root, "inventory-sources.json")
	raw, err := os.ReadFile(inventory)
	if err != nil {
		return fmt.Errorf("snapshot requires inventory-sources.json: %w", err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return errors.New("snapshot inventory-sources.json must be an object")
	}
	if err := copyDeployAzureFile(inventory, filepath.Join(destination, "inventory-sources.json")); err != nil {
		return err
	}
	return copyDeployAzureFile(filepath.Join(root, "payload-hashes.json"), filepath.Join(destination, "payload-hashes.json"))
}
