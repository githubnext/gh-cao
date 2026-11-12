package topology

import (
	"fmt"
	"strings"
)

func renderManifest(t Topology) string {
	var out strings.Builder
	out.WriteString("# Configuration topology manifest\n\n")
	out.WriteString("**Azure / TOSCA Simple Profile 1.3 / CAO feature dependency graph.**\n\n")
	out.WriteString("[topology.yaml](topology.yaml) is the generated service template. The bounded\nCAO graph validates dependency closure and cycles; Gonum determines bootstrap order.\ntopology.json is its deterministic projection; manifest.json hashes every generated document.\n\n")
	out.WriteString("| Choice | Selected |\n| --- | --- |\n")
	for _, row := range []struct {
		name  string
		value any
	}{
		{"deployment", t.Features.Deployment},
		{"acquisition", t.Features.Acquisition}, {"ingress", t.Features.Ingress},
		{"analytics", t.Features.Analytics}, {"telemetry", t.Features.Telemetry},
		{"sessionDataLake", t.Features.SessionDataLake}, {"webhookGateway", t.Features.WebhookGateway},
	} {
		fmt.Fprintf(&out, "| %s | %v |\n", row.name, row.value)
	}
	out.WriteString("\n## Architecture and authority boundaries\n\nSolid arrows show baseline connections; dashed arrows show external prerequisites\nor gated optional flows, NOT proof of activation. Secret values never appear.\n\n```mermaid\n")
	out.WriteString(architecture(t.Features))
	out.WriteString("\n```\n\n## Resolved feature dependency topology\n\nArrows point from prerequisite to consumer (the reverse of generated TOSCA's consumer-to-target\nDependsOn assignment). External nodes remain UNVERIFIED. This is bootstrap order,\nnot replacement ARM scheduling.\n\n```mermaid\nflowchart TD\n")
	ids := map[string]string{}
	for index, node := range t.Nodes {
		ids[node.ID] = fmt.Sprintf("n%d", index)
	}
	for _, kind := range []string{"managed", "external"} {
		title := "Managed configuration / infrastructure"
		if kind == "external" {
			title = "External prerequisites: UNVERIFIED"
		}
		fmt.Fprintf(&out, "  subgraph %s[\"%s\"]\n", kind, title)
		for _, node := range t.Nodes {
			if node.Kind == kind {
				fmt.Fprintf(&out, "    %s[\"%s\"]\n", ids[node.ID], node.ID)
			}
		}
		out.WriteString("  end\n")
	}
	for _, node := range t.Nodes {
		for _, dependency := range node.DependsOn {
			fmt.Fprintf(&out, "  %s --> %s\n", ids[dependency], ids[node.ID])
		}
	}
	out.WriteString("```\n\n## Configuration and activation\n\nReview [README.md](README.md), [TODO.md](TODO.md) and [GITHUB-APPS.md](GITHUB-APPS.md).\n")
	out.WriteString("\n[AGENT-MCP.md](AGENT-MCP.md) documents Actions agents using BOTH a bearer GITHUB_TOKEN\nand signed OIDC provenance in X-GitHub-OIDC-Token, with audience https://cao.githubnext.com.\nThis optional read-only path requires separate host activation and repository/default-branch\npermission checks; OIDC alone grants no access. The unchanged Functions baseline does NOT wire MCP.\n")
	if t.Features.SessionDataLake || t.Features.WebhookGateway {
		out.WriteString("Additional host resources and activation gates: [OPTIONAL-FEATURES.md](OPTIONAL-FEATURES.md).\n")
	}
	out.WriteString("\nGeneration and offline checking do not prove live reachability, secret access,\nApp installation, policy approval or destination conformance.\n")
	return out.String()
}

func architecture(f Features) string {
	if f.Deployment == "azure-container-apps" {
		return containerAppsArchitecture(f)
	}
	if f.Deployment != "azure-functions" {
		return compactArchitecture(f)
	}
	lines := []string{
		"flowchart LR", `  browser["Authorized browser"]`, `  signin["GitHub browser sign-in App"]`,
		`  subgraph baseline["Azure baseline"]`,
		`    dashboard["Functions: dashboard + OAuth<br/>one HTTP origin"]`,
		`    postgres["Private PostgreSQL<br/>canonical + isolated operational state"]`,
		`    vault["Key Vault<br/>secret-scoped managed identities"]`,
		`    registry["ACR<br/>reviewed runtime and job images"]`,
		`    storage["Functions runtime storage<br/>NOT the session lake"]`,
		`    provision["Manual DB provisioning<br/>separate privileged identity"]`, "  end",
		"  signin <-->|OAuth authorization code| dashboard",
		"  dashboard -->|runtime login, verified TLS| postgres",
		"  dashboard -->|managed identity, secret references| vault",
		"  registry -->|managed identity pull| dashboard", "  dashboard --> storage",
		"  provision -->|bootstrap only| postgres",
	}
	if f.Ingress == "private" {
		lines = append(lines, `  privateIngress["Approved private connectivity<br/>EXTERNAL prerequisite"]`, "  browser --> privateIngress", "  privateIngress -.->|HTTPS + OAuth| dashboard")
	} else {
		lines = append(lines, "  browser -->|public HTTPS, OAuth required| dashboard")
	}
	if f.Acquisition == "collector" {
		lines = append(lines,
			`  collectionApp["Installation-scoped collection App"]`,
			`  workers["Private collection workers + backfill<br/>App key, reviewed inventory"]`,
			"  collectionApp -->|signed webhooks| dashboard", "  dashboard -->|admit only, no App private key| postgres",
			"  postgres -->|durable collection queue| workers", "  collectionApp -->|authorized evidence| workers",
			"  workers -->|canonical publication| postgres", "  vault -->|secret-scoped access| workers")
	} else {
		lines = append(lines, `  snapshot["Verified Actions snapshot<br/>OR explicitly empty evidence"]`,
			`  ingestion["Manual ingestion job<br/>runtime-only identity"]`,
			"  snapshot --> ingestion", "  provision -.->|must succeed first| ingestion", "  ingestion --> postgres")
	}
	if f.Analytics == "adx" {
		lines = append(lines, `  analyticsAdx["Paid private ADX destination<br/>NO exporter enabled"]`)
	}
	if f.Telemetry == "otlp" {
		lines = append(lines, `  otlp["Operator-managed HTTPS OTLP collector<br/>EXTERNAL prerequisite"]`, "  dashboard -.->|opt-in telemetry| otlp")
		if f.Acquisition == "collector" {
			lines = append(lines, "  workers -.->|opt-in telemetry| otlp")
		}
	}
	if f.SessionDataLake || f.WebhookGateway {
		lines = append(lines, `  subgraph optional["Separate peered VNet: optional infrastructure, runtimes STAGED"]`)
		if f.SessionDataLake {
			lines = append(lines, `    archive["Single persistent archive VM<br/>private managed data disk"]`,
				`    blob["Dedicated private Blob<br/>immutable Parquet + generations"]`,
				`    lakeAdx["Dedicated private ADX<br/>generation-bound external table"]`,
				"    archive -.->|conditional publication after approval| blob",
				"    archive -.->|approved bounded queries| lakeAdx",
				"    lakeAdx -.->|managed private endpoint, reader identity| blob")
		}
		if f.WebhookGateway {
			lines = append(lines, `    receiver["Gateway receive VM<br/>TLS + HMAC, NO App private keys"]`,
				`    dispatcher["Gateway dispatch VM<br/>source + dispatch App keys, NO HMAC"]`)
		}
		lines = append(lines, "  end")
	}
	if f.SessionDataLake {
		lines = append(lines,
			`  archiveApproval["Content policy + source authority + readers<br/>real Blob/ADX acceptance: EXTERNAL gates"]`,
			`  archiveSignin["SEPARATE archive browser sign-in App"]`,
			"  archiveApproval -.->|activation required| archive", "  archiveSignin -.->|separate OAuth callback| archive",
			"  browser -.->|approved full-content readers only| archive")
	}
	if f.WebhookGateway {
		lines = append(lines, `  sources["Explicitly enrolled GitHub/provider sources"]`,
			`  gatewayPolicy["Reviewed exact-revision gateway policy<br/>Apps + grants: EXTERNAL gates"]`,
			`  gatewayQueue["Gateway queue<br/>isolated namespace in SAME PostgreSQL"]`,
			`  workflows["Approved private control workflows<br/>CAO policy + gh-aw ceilings still apply"]`,
			"  sources -.->|signed HTTPS deliveries| receiver", "  gatewayPolicy -.-> receiver",
			"  gatewayPolicy -.-> dispatcher", "  receiver -.->|bounded admission| gatewayQueue",
			"  gatewayQueue -.->|operational storage only| postgres",
			"  gatewayQueue -.->|fenced claims, fresh authorization| dispatcher",
			"  dispatcher -.->|only approved dispatch transport| workflows")
	}
	return strings.Join(append(lines, agentMCPArchitecture(f)...), "\n")
}

func containerAppsArchitecture(f Features) string {
	lines := []string{
		"flowchart LR",
		`  browser["Authorized browser"]`,
		`  signin["GitHub OAuth App<br/>explicit allowlist"]`,
		`  approval["Successful provisioning + ingestion + reviewed runtime activation<br/>EXTERNAL gates"]`,
		`  subgraph network["Azure VNet"]`,
		`    environment["Container Apps Consumption environment<br/>managed HTTPS ingress"]`,
		`    dashboard["Go serve-hosted: ONE replica, activation gated<br/>dashboard + OAuth + server queries, one origin"]`,
		`    postgres["Private PostgreSQL 16<br/>canonical + isolated operational state"]`,
		`    provision["Manual DB provisioning job<br/>privileged identity"]`,
		`    ingestion["Manual snapshot ingestion job<br/>runtime-only identity"]`,
		"  end",
		`  registry["ACR<br/>three reviewed image digests"]`,
		`  vault["Key Vault<br/>separate secret-scoped identities"]`,
		`  monitoring["Log Analytics<br/>container logs"]`,
		"  browser -->|HTTPS only| environment",
		"  environment -.->|verified private proxy peers, after activation| dashboard",
		"  signin <-->|OAuth authorization code| dashboard",
		"  approval -.-> dashboard",
		"  dashboard -->|runtime login, verified TLS| postgres",
		"  provision -->|bootstrap only| postgres",
		"  provision -.->|must succeed first| ingestion",
		"  ingestion -->|verified snapshot OR deliberately empty evidence| postgres",
		"  registry -->|managed identity pull| dashboard",
		"  registry --> provision",
		"  registry --> ingestion",
		"  vault -->|runtime secrets ONLY| dashboard",
		"  vault -->|bootstrap secrets ONLY| provision",
		"  vault -->|runtime URL ONLY| ingestion",
		"  environment --> monitoring",
	}
	return strings.Join(append(lines, agentMCPArchitecture(f)...), "\n")
}

func compactArchitecture(f Features) string {
	lines := []string{
		"flowchart LR",
		`  browser["Authorized browser"]`,
		`  oauth["GitHub OAuth App<br/>explicit allowlist"]`,
		`  vault["Existing Key Vault<br/>credentials + TLS keys, EXTERNAL"]`,
		`  approval["Reviewed policy + binary + secrets + schema<br/>runtime activation remains external"]`,
		`  subgraph network["Azure VNet + approved HTTPS source CIDRs"]`,
		`    subgraph vm["ONE VM: hosted runtime STAGED"]`,
		`      dashboard["Go serve-hosted<br/>direct HTTPS + OAuth"]`,
		`      disk["Persistent managed data disk"]`,
	}
	if f.Deployment == "single-vm" {
		lines = append(lines, `      postgres["Local PostgreSQL 16<br/>Unix socket / loopback ONLY"]`, "      postgres -.->|data directory after disk setup| disk")
	}
	lines = append(lines, "    end")
	if f.Deployment == "minimal-azure" {
		lines = append(lines, `    postgres["Private Azure PostgreSQL<br/>delegated subnet + private DNS"]`)
	}
	lines = append(lines, "  end",
		"  browser -.->|HTTPS 443 only, after activation| dashboard",
		"  oauth -.->|authorization-code flow| dashboard",
		"  vault -.->|approved secret-scoped delivery| dashboard",
		"  approval -.-> dashboard",
		"  dashboard -.->|runtime-only database login| postgres",
		`  snapshots["Verified Actions snapshot OR deliberately empty evidence"]`,
		"  snapshots -.->|owner bootstrap, then runtime ingestion| postgres")
	return strings.Join(append(lines, agentMCPArchitecture(f)...), "\n")
}

func agentMCPArchitecture(f Features) []string {
	endpoint := "Optional Go /mcp<br/>separate serve-hosted activation"
	if f.Deployment == "azure-functions" {
		endpoint = "Go /mcp contract: NOT WIRED<br/>unchanged Functions entrypoint"
	}
	return []string{
		`  actionsAgent["Agent in GitHub Actions job<br/>default-branch caller only"]`,
		`  oidcIssuer["GitHub Actions OIDC issuer<br/>short-lived signed provenance"]`,
		`  mcpAPI["GitHub API<br/>repository identity + read permission probes"]`,
		`  mcpGate["Reviewed caller repository + host opt-in<br/>external activation prerequisites"]`,
		`  agentMCP["` + endpoint + `"]`,
		"  actionsAgent -.->|mint for audience https://cao.githubnext.com| oidcIssuer",
		"  oidcIssuer -.->|OIDC JWT| actionsAgent",
		"  actionsAgent -.->|HTTPS: bearer GITHUB_TOKEN AND X-GitHub-OIDC-Token| agentMCP",
		"  mcpGate -.-> agentMCP",
		"  agentMCP -.->|verify issuer signature and time claims| oidcIssuer",
		"  agentMCP -.->|current default branch, IDs and token permissions| mcpAPI",
		"  agentMCP -.->|same Go host, reviewed read-only tools ONLY| dashboard",
	}
}
