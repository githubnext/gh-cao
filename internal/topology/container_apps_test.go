package topology

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func containerAppsRequirements(t *testing.T) Requirements {
	t.Helper()
	content, err := ExampleFor("azure-container-apps")
	if err != nil {
		t.Fatal(err)
	}
	input, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestContainerAppsValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Requirements)
		want   string
	}{
		{"literal-secret", func(r *Requirements) { r.Azure.Parameters["sessionSecret"] = "not-allowed" }, "SEC-VAULT-002"},
		{"missing-vault-secret", func(r *Requirements) { delete(r.Azure.Secrets, "githubWebhookSecret") }, "SEC-VAULT-001"},
		{"unknown-secret", func(r *Requirements) { r.Azure.Secrets["collectorPrivateKey"] = "extra-key" }, "unknown Container Apps secret"},
		{"shared-database-password", func(r *Requirements) {
			r.Azure.Secrets["postgresOwnerPassword"] = r.Azure.Secrets["postgresRuntimePassword"]
		}, "distinct vault secret"},
		{"mutable-image", func(r *Requirements) { r.Azure.Parameters["dashboardImage"] = "examplecaoacr.azurecr.io/server:latest" }, "immutable"},
		{"missing-image-repository", func(r *Requirements) {
			r.Azure.Parameters["dashboardImage"] = "examplecaoacr.azurecr.io/@sha256:" + strings.Repeat("a", 64)
		}, "immutable"},
		{"foreign-registry", func(r *Requirements) {
			r.Azure.Parameters["ingestionImage"] = "other.azurecr.io/ingest@sha256:" + strings.Repeat("a", 64)
		}, "selected ACR"},
		{"missing-image", func(r *Requirements) { delete(r.Azure.Parameters, "provisioningImage") }, "immutable"},
		{"invalid-name", func(r *Requirements) { r.Azure.Parameters["deploymentName"] = "invalid--name" }, "invalid Azure resource name"},
		{"missing-location", func(r *Requirements) { delete(r.Azure.Parameters, "location") }, "dependency missing: location"},
		{"missing-proxy-policy", func(r *Requirements) { delete(r.Azure.Parameters, "trustedProxyCidrs") }, "reviewed trustedProxyCidrs"},
		{"public-proxy", func(r *Requirements) { r.Azure.Parameters["trustedProxyCidrs"] = []string{"0.0.0.0/0"} }, "private IPv4 or loopback"},
		{"overbroad-proxy", func(r *Requirements) { r.Azure.Parameters["trustedProxyCidrs"] = []string{"10.0.0.0/7"} }, "private IPv4 or loopback"},
		{"malformed-proxy", func(r *Requirements) { r.Azure.Parameters["trustedProxyCidrs"] = []string{"not-a-cidr"} }, "private IPv4 or loopback"},
		{"no-oauth-allowlist", func(r *Requirements) { r.Azure.Parameters["githubAllowedOrganizations"] = []string{} }, "at least one allowed"},
		{"missing-retention", func(r *Requirements) { delete(r.Azure.Parameters, "postgresRunRetentionDays") }, "explicit postgresRunRetentionDays"},
		{"invalid-retention", func(r *Requirements) { r.Azure.Parameters["postgresRunRetentionDays"] = 1 }, "minValue"},
		{"collector", func(r *Requirements) { r.Features.Acquisition = "collector" }, "Actions snapshots only"},
		{"private", func(r *Requirements) { r.Features.Ingress = "private" }, "Actions snapshots only"},
		{"adx", func(r *Requirements) { r.Features.Analytics = "adx" }, "Actions snapshots only"},
		{"telemetry", func(r *Requirements) { r.Features.Telemetry = "otlp" }, "Actions snapshots only"},
		{"lake", func(r *Requirements) { r.Features.SessionDataLake = true }, "Actions snapshots only"},
		{"gateway", func(r *Requirements) { r.Features.WebhookGateway = true }, "Actions snapshots only"},
		{"vm-hosts", func(r *Requirements) { r.Azure.Hosts = &Hosts{} }, "azure.hosts"},
		{"functions-parameter", func(r *Requirements) { r.Azure.Parameters["functionAppName"] = "unexpected" }, "unknown or secret Azure parameter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := containerAppsRequirements(t)
			test.change(&input)
			if _, err := Solve(context.Background(), input); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("wanted %q, got %v", test.want, err)
			}
		})
	}
}

func TestContainerAppsBundleAndActivation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		input := containerAppsRequirements(t)
		input.Azure.Parameters["runtimeEnabled"] = enabled
		content, err := yaml.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		files, graph, err := filesFor(context.Background(), content)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range graph.Nodes {
			if slices.Contains([]string{"vm", "managed-disk", "runtime-storage"}, node.ID) {
				t.Fatalf("Functions/VM dependency leaked into Container Apps: %s", node.ID)
			}
		}
		for _, required := range []string{"postgres", "network", "registry", "monitoring", "job-environment", "database-provisioning", "snapshot-ingestion", "runtime-activation"} {
			if !slices.ContainsFunc(graph.Nodes, func(n Node) bool { return n.ID == required }) {
				t.Fatalf("missing dependency: %s", required)
			}
		}
		if slices.Index(graph.Order, "snapshot-ingestion") >= slices.Index(graph.Order, "dashboard") {
			t.Fatal("web activation must follow ingestion")
		}
		var raw struct {
			Parameters map[string]struct {
				Value     any              `json:"value"`
				Reference *SecretReference `json:"reference"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(files["parameters.json"], &raw); err != nil {
			t.Fatal(err)
		}
		for _, name := range containerAppsSecretNames {
			ref := raw.Parameters[name].Reference
			if ref == nil || ref.KeyVault.ID != input.Azure.SecretVaultID || ref.SecretName != input.Azure.Secrets[name] {
				t.Fatalf("lost vault reference for %s", name)
			}
		}
		if raw.Parameters["runtimeEnabled"].Value != enabled {
			t.Fatal("runtime activation differs from reviewed input")
		}
		for _, required := range []string{"Container Apps Consumption", "Go serve-hosted", "Private PostgreSQL", "runtime activation"} {
			if !strings.Contains(string(files["MANIFEST.md"]), required) {
				t.Fatalf("missing architecture boundary %q", required)
			}
		}
		for _, name := range []string{"README.md", "TODO.md", "GITHUB-APPS.md"} {
			if !strings.Contains(string(files[name]), "AGENT-MCP.md") {
				t.Fatalf("%s omits MCP boundary", name)
			}
		}
	}
}

func TestContainerAppsCompiledContract(t *testing.T) {
	files, err := deploymentFiles(Features{Deployment: "azure-container-apps"})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	compiledJSON := compileSampleJSON(t, filepath.Join(directory, "main.bicep"))
	var compiled sampleARM
	if err := json.Unmarshal(compiledJSON, &compiled); err != nil {
		t.Fatal(err)
	}
	var resources []struct {
		Type       string         `json:"type"`
		Condition  string         `json:"condition"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(compiled.Resources, &resources); err != nil {
		t.Fatal(err)
	}
	var web, environment map[string]any
	for _, resource := range resources {
		switch resource.Type {
		case "Microsoft.App/containerApps":
			if resource.Condition != "[parameters('runtimeEnabled')]" {
				t.Fatal("web app is not gated by explicit runtime activation")
			}
			web = resource.Properties
		case "Microsoft.App/managedEnvironments":
			environment = resource.Properties
		}
	}
	if web == nil || environment == nil {
		t.Fatal("missing web app or environment")
	}
	configuration := web["configuration"].(map[string]any)
	ingress := configuration["ingress"].(map[string]any)
	if ingress["allowInsecure"] != false || ingress["external"] != true || ingress["targetPort"] != float64(8080) {
		t.Fatal("managed HTTPS ingress contract changed")
	}
	template := web["template"].(map[string]any)
	scale := template["scale"].(map[string]any)
	if scale["minReplicas"] != float64(1) || scale["maxReplicas"] != float64(1) {
		t.Fatal("single-replica activation contract changed")
	}
	container := template["containers"].([]any)[0].(map[string]any)
	args, _ := list(container["args"])
	if len(args) == 0 || args[0] != "serve-hosted" || !slices.Contains(args, "postgres") || slices.Contains(args, "--mcp-enabled") {
		t.Fatal("runtime must use hosted PostgreSQL without enabling MCP")
	}
	env := map[string]map[string]any{}
	for _, entry := range container["env"].([]any) {
		setting := entry.(map[string]any)
		env[setting["name"].(string)] = setting
	}
	if env["CAO_POSTGRES_URL"]["secretRef"] != "[variables('postgresUrlSecretName')]" ||
		env["CAO_TRUSTED_PROXY_CIDRS"]["value"] != "[join(parameters('trustedProxyCidrs'), ',')]" ||
		env["CAO_EXTERNAL_INGESTION"]["value"] != "true" || env["CAO_SOURCE_DIRECTORY"] != nil {
		t.Fatal("runtime credential/proxy boundary changed")
	}
	for _, forbidden := range []string{"CAO_DATABASE_ADMIN_URL", "CAO_POSTGRES_OWNER_PASSWORD", "CAO_POSTGRES_RUNTIME_PASSWORD"} {
		if env[forbidden] != nil {
			t.Fatalf("web received bootstrap credentials: %s", forbidden)
		}
	}
	// Compare the entire compiled contract, not just its parameter names.
	embedded, err := assets.ReadFile("assets/container-apps/main.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected any
	if err := json.Unmarshal(compiledJSON, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(embedded, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("stale Container Apps contract; run go generate ./internal/topology")
	}
	if bytes.Contains(embedded, []byte("Microsoft.Web/")) || bytes.Contains(embedded, []byte("Microsoft.Storage/")) {
		t.Fatal("Functions/Premium/Storage resources leaked into Container Apps")
	}
}
