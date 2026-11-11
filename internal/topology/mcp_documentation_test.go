package topology

import (
	"context"
	"strings"
	"testing"
)

func TestSamplesDocumentActionsOIDCMCPWithoutEnablingIt(t *testing.T) {
	for _, profile := range []string{"single-vm", "minimal-azure", "azure-functions", "azure-container-apps", "full-azure"} {
		t.Run(profile, func(t *testing.T) {
			source, err := ExampleFor(profile)
			if err != nil {
				t.Fatal(err)
			}
			files, graph, err := filesFor(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			document := string(files["AGENT-MCP.md"])
			for _, required := range []string{
				"OIDC alone is not sufficient",
				"Authorization: Bearer",
				"X-GitHub-OIDC-Token:",
				"https://cao.githubnext.com",
				"https://token.actions.githubusercontent.com",
				"id-token: write",
				"actions: read", "contents: read", "issues: read", "pull-requests: read",
				"CAO_MCP_ACTIONS_REPOSITORY",
				"CURRENT", "default branch",
				"Pull-request, environment and non-default-branch",
				"Azure Functions baseline does NOT wire hosted MCP",
				"must NOT be added to `azure.secrets`",
			} {
				if !strings.Contains(document, required) {
					t.Fatalf("missing agent authentication boundary %q", required)
				}
			}
			manifest := string(files["MANIFEST.md"])
			for _, required := range []string{"Agent in GitHub Actions", "OIDC issuer", "bearer GITHUB_TOKEN AND X-GitHub-OIDC-Token", "AGENT-MCP.md"} {
				if !strings.Contains(manifest, required) {
					t.Fatalf("architecture omits %q", required)
				}
			}
			unwired := strings.Contains(manifest, "Go /mcp contract: NOT WIRED")
			if unwired != (graph.Features.Deployment == "azure-functions") {
				t.Fatal("diagram misrepresents the host's MCP activation capability")
			}
			if !strings.Contains(string(files["topology.yaml"]), "agent_mcp_activation: documented-not-enabled") {
				t.Fatal("TOSCA implies activation or omits the MCP contract reference")
			}
			for _, name := range []string{"README.md", "TODO.md"} {
				if !strings.Contains(string(files[name]), "AGENT-MCP.md") {
					t.Fatalf("%s omits the agent MCP setup dependency", name)
				}
			}
			parameters := string(files["parameters.json"])
			if strings.Contains(parameters, "CAO_MCP_ACTIONS_REPOSITORY") || strings.Contains(parameters, "mcp-enabled") {
				t.Fatal("documentation change silently enabled or configured MCP")
			}
		})
	}
}
