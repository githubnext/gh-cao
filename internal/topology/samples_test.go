package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// These are the same inputs shipped by `topology init --deployment`.
// Missing Bicep is a failure, not skipped validation.
func TestDeploymentSamples(t *testing.T) {
	for _, sample := range []struct {
		name                      string
		vms, databases, functions int
		managedDatabase           bool
	}{
		{"single-vm", 1, 0, 0, false},
		{"minimal-azure", 1, 1, 0, true},
		{"azure-functions", 0, 1, 1, true},
		{"full-azure", 3, 1, 1, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			content, err := ExampleFor(sample.name)
			if err != nil {
				t.Fatal(err)
			}
			input, err := Parse(content)
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(t.TempDir(), "bundle")
			var diagnostics bytes.Buffer
			graph, err := Generate(context.Background(), content, directory, BicepCLI{Diagnostics: &diagnostics})
			if err != nil {
				t.Fatalf("%v\n%s", err, &diagnostics)
			}
			if diagnostics.Len() > 0 {
				t.Log(diagnostics.String())
			}
			if _, err := Check(context.Background(), directory); err != nil {
				t.Fatal(err)
			}
			report, err := ReadRuleReport(directory)
			if err != nil || report.Errors != 0 || report.Warnings == 0 {
				t.Fatalf("expected explicit advisory tradeoffs, got %+v: %v", report, err)
			}
			counts := map[string]int{}
			countCompiledResources(t, compileSample(t, filepath.Join(directory, "main.bicep")), sample.managedDatabase, counts)
			if sample.name == "full-azure" {
				countCompiledResources(t, compileSample(t, filepath.Join(directory, "optional", "hosts.bicep")), true, counts)
			}
			for kind, expected := range map[string]int{
				"Microsoft.Compute/virtualMachines":         sample.vms,
				"Microsoft.DBforPostgreSQL/flexibleServers": sample.databases,
				"Microsoft.Web/sites":                       sample.functions,
			} {
				if counts[kind] != expected {
					t.Fatalf("%s: got %d %s resources, want %d", sample.name, counts[kind], kind, expected)
				}
			}
			if input.Features.Deployment != "azure-functions" {
				for _, absent := range []string{"Microsoft.ContainerRegistry/registries", "Microsoft.KeyVault/vaults", "Microsoft.App/jobs", "Microsoft.Kusto/clusters"} {
					if counts[absent] != 0 {
						t.Fatalf("compact profile accidentally carries %s", absent)
					}
				}
				index := slices.IndexFunc(graph.Nodes, func(node Node) bool { return node.ID == "postgres" })
				if index < 0 {
					t.Fatal("missing PostgreSQL dependency")
				}
				onVM := slices.Contains(graph.Nodes[index].DependsOn, "vm")
				if onVM != (sample.name == "single-vm") {
					t.Fatal("local vs managed PostgreSQL topology is incorrect")
				}
			}
			if info, err := os.Stat(filepath.Join(directory, "RULES.md")); err != nil || info.Size() == 0 {
				t.Fatalf("missing rule review: %v", err)
			}
		})
	}
}

type sampleARM struct {
	Resources json.RawMessage `json:"resources"`
}

type sampleResource struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Condition  string `json:"condition"`
	Properties struct {
		Template *sampleARM `json:"template"`
	} `json:"properties"`
}

func compileSample(t *testing.T, filename string) sampleARM {
	t.Helper()
	command := exec.Command("bicep", "build", filename, "--no-restore", "--stdout")
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		t.Fatalf("official Bicep compilation failed: %v\n%s", err, &diagnostics)
	}
	var template sampleARM
	if err := json.Unmarshal(output, &template); err != nil {
		t.Fatal(err)
	}
	return template
}

func countCompiledResources(t *testing.T, template sampleARM, managedDatabase bool, counts map[string]int) {
	t.Helper()
	var resources []sampleResource
	if len(template.Resources) == 0 {
		t.Fatal("compiler omitted resources")
	}
	if template.Resources[0] == '[' {
		if err := json.Unmarshal(template.Resources, &resources); err != nil {
			t.Fatal(err)
		}
	} else {
		var values map[string]sampleResource
		if err := json.Unmarshal(template.Resources, &values); err != nil {
			t.Fatal(err)
		}
		for _, resource := range values {
			resources = append(resources, resource)
		}
	}
	for _, resource := range resources {
		if resource.Name == "compact-managed-postgres" {
			if resource.Condition != "[parameters('managedPostgres')]" {
				t.Fatalf("unexpected managed-database condition: %s", resource.Condition)
			}
			if !managedDatabase {
				continue
			}
		}
		counts[resource.Type]++
		if resource.Type == "Microsoft.Resources/deployments" && resource.Properties.Template != nil {
			countCompiledResources(t, *resource.Properties.Template, managedDatabase, counts)
		}
	}
}
