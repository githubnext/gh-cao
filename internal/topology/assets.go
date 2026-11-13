// Package topology solves offline configuration dependencies and emits review-only deployment bundles.
package topology

import (
	"embed"
	"fmt"
)

//go:generate go run ./cmd/generate-assets

//go:embed assets
var assets embed.FS

// AzureTemplate returns the embedded, compiled Functions baseline.
func AzureTemplate() ([]byte, error) {
	return assets.ReadFile("assets/azure-baseline.generated/main.json")
}

func baselineFiles() (map[string][]byte, error) {
	files := baselineBicepFiles()
	content, err := AzureTemplate()
	if err != nil {
		return nil, err
	}
	files["main.json"] = content
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
		return map[string][]byte{
			"main.bicep":         []byte(containerAppsMainBicep),
			"postgres.bicep":     []byte(compactPostgresBicep),
			"provisioning.bicep": []byte(azureProvisioningBicep),
			"ingestion.bicep":    []byte(azureIngestionBicep),
			"worker-roles.bicep": []byte(azureWorkerRolesBicep),
		}, nil
	}
	if features.Deployment == "azure-functions" {
		return baselineBicepFiles(), nil
	}
	files := map[string][]byte{
		"main.bicep":     []byte(compactMainBicep),
		"postgres.bicep": []byte(compactPostgresBicep),
		"vm-host.bicep":  []byte(optionalHostBicep),
	}
	return files, addHostInitFiles(files)
}

func optionalFiles() (map[string][]byte, error) {
	files := map[string][]byte{
		"host.bicep":    []byte(optionalHostBicep),
		"hosts.bicep":   []byte(optionalHostsBicep),
		"lake.bicep":    []byte(optionalLakeBicep),
		"network.bicep": []byte(optionalNetworkBicep),
	}
	return files, addHostInitFiles(files)
}

func addHostInitFiles(files map[string][]byte) error {
	for _, name := range []string{"host-init.yaml", "host-init-postgres.yaml"} {
		content, err := assets.ReadFile("assets/azure/" + name)
		if err != nil {
			return err
		}
		files[name] = content
	}
	return nil
}
