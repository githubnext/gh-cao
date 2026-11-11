// Package topology solves offline configuration dependencies and emits review-only deployment bundles.
package topology

import (
	"embed"
	"fmt"
)

//go:generate go run ./cmd/generate-assets

//go:embed assets
var assets embed.FS

func baselineFiles() (map[string][]byte, error) {
	const directory = "assets/azure-baseline.generated"
	entries, err := assets.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("unexpected directory in generated Azure baseline: %s", entry.Name())
		}
		content, err := assets.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, err
		}
		files[entry.Name()] = content
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
	case "single-vm", "minimal-azure", "full-azure", "azure-container-apps":
		return assets.ReadFile("assets/examples/" + deployment + ".yaml")
	default:
		return nil, fmt.Errorf("unsupported deployment example: %s", deployment)
	}
}

func deploymentFiles(features Features) (map[string][]byte, error) {
	if features.Deployment == "azure-container-apps" {
		files := map[string][]byte{}
		for name, source := range map[string]string{
			"main.bicep":         "assets/container-apps/main.bicep",
			"postgres.bicep":     "assets/compact/postgres.bicep",
			"provisioning.bicep": "assets/azure-baseline.generated/provisioning.bicep",
			"ingestion.bicep":    "assets/azure-baseline.generated/ingestion.bicep",
			"worker-roles.bicep": "assets/azure-baseline.generated/worker-roles.bicep",
		} {
			content, err := assets.ReadFile(source)
			if err != nil {
				return nil, err
			}
			files[name] = content
		}
		return files, nil
	}
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
