// Package topology solves offline configuration dependencies and emits review-only deployment bundles.
package topology

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
)

//go:generate go run ./cmd/generate-assets

//go:embed azure-baseline.generated.json.gz assets
var assets embed.FS

func baselineFiles() (map[string][]byte, error) {
	content, err := assets.ReadFile("azure-baseline.generated.json.gz")
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	var files map[string][]byte
	if err := json.NewDecoder(io.LimitReader(reader, 4<<20)).Decode(&files); err != nil {
		return nil, fmt.Errorf("decode embedded Azure baseline: %w", err)
	}
	return files, nil
}

func Example() ([]byte, error) {
	return ExampleFor("azure-functions")
}

func ExampleFor(deployment string) ([]byte, error) {
	switch deployment {
	case "azure-functions":
		return assets.ReadFile("assets/example.yaml")
	case "single-vm", "minimal-azure", "full-azure":
		return assets.ReadFile("assets/examples/" + deployment + ".yaml")
	default:
		return nil, fmt.Errorf("unsupported deployment example: %s", deployment)
	}
}

func deploymentFiles(features Features) (map[string][]byte, error) {
	if features.Deployment == "azure-functions" {
		baseline, err := baselineFiles()
		if err != nil {
			return nil, err
		}
		delete(baseline, "main.json")
		return baseline, nil
	}
	files := map[string][]byte{}
	for name, source := range map[string]string{
		"main.bicep":              "assets/compact/main.bicep",
		"postgres.bicep":          "assets/compact/postgres.bicep",
		"vm-host.bicep":           "assets/azure/host.bicep",
		"host-init.yaml":          "assets/azure/host-init.yaml",
		"host-init-postgres.yaml": "assets/azure/host-init-postgres.yaml",
	} {
		content, err := assets.ReadFile(source)
		if err != nil {
			return nil, err
		}
		files[name] = content
	}
	return files, nil
}
