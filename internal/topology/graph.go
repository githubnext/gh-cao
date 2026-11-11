package topology

import (
	"fmt"
	"maps"
	"slices"

	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/simple"
	"gonum.org/v1/gonum/graph/topo"
)

var contracts = map[string][]string{
	"network": {}, "postgres": {"network"}, "registry": {}, "monitoring": {}, "runtime-storage": {},
	"sign-in-app": {}, "secrets": {"sign-in-app"}, "job-environment": {"network", "monitoring"},
	"database-provisioning": {"postgres", "secrets", "registry", "job-environment"},
	"dashboard":             {"database-provisioning", "registry", "monitoring", "runtime-storage", "secrets"},
	"snapshot-ingestion":    {"database-provisioning", "job-environment", "registry", "secrets"},
	"collection-app":        {}, "reviewed-inventory": {"database-provisioning", "collection-app"},
	"collection": {"dashboard", "reviewed-inventory", "collection-app", "job-environment", "registry", "secrets"},
	"adx":        {"network"}, "private-connectivity": {}, "otlp-collector": {},
	"telemetry-export": {"dashboard", "otlp-collector"}, "feature-network": {"network"},
	"archive-host": {"feature-network", "registry"}, "archive-blob": {"feature-network", "archive-host"},
	"archive-adx": {"feature-network", "archive-blob"}, "archive-approval": {}, "archive-acquisition": {},
	"session-data-lake": {"archive-host", "archive-blob", "archive-adx", "archive-approval", "archive-acquisition", "sign-in-app"},
	"gateway-apps":      {}, "gateway-policy": {}, "gateway-receive-secrets": {}, "gateway-dispatch-secrets": {},
	"gateway-receive":  {"feature-network", "registry", "database-provisioning", "gateway-policy", "gateway-apps", "gateway-receive-secrets", "gateway-dispatch"},
	"gateway-dispatch": {"feature-network", "registry", "database-provisioning", "gateway-policy", "gateway-apps", "gateway-dispatch-secrets"},
	"webhook-gateway":  {"gateway-receive", "gateway-dispatch"},
	"vm":               {"network", "managed-disk"}, "managed-disk": {},
	"runtime-secrets": {}, "runtime-image": {}, "runtime-activation": {},
}

var external = map[string]bool{
	"sign-in-app": true, "collection-app": true, "reviewed-inventory": true,
	"private-connectivity": true, "otlp-collector": true, "archive-approval": true, "archive-acquisition": true,
	"gateway-apps": true, "gateway-policy": true, "gateway-receive-secrets": true, "gateway-dispatch-secrets": true,
	"runtime-secrets": true, "runtime-image": true, "runtime-activation": true,
}

func (f Features) validate() error {
	for name, pair := range map[string]struct {
		value   string
		choices []string
	}{
		"deployment":  {f.Deployment, []string{"single-vm", "minimal-azure", "azure-functions"}},
		"acquisition": {f.Acquisition, []string{"actions", "collector"}},
		"ingress":     {f.Ingress, []string{"public", "private"}},
		"analytics":   {f.Analytics, []string{"none", "adx"}},
		"telemetry":   {f.Telemetry, []string{"none", "otlp"}},
	} {
		if !slices.Contains(pair.choices, pair.value) {
			return fmt.Errorf("unsupported features.%s", name)
		}
	}
	if f.Deployment != "azure-functions" && (f.Acquisition != "actions" || f.Ingress != "public" || f.Analytics != "none" || f.Telemetry != "none" || f.SessionDataLake || f.WebhookGateway) {
		return fmt.Errorf("compact deployments support public hosted OAuth with Actions snapshots only; select azure-functions for optional managed features")
	}
	return nil
}

func dependencies(id string, f Features) []string {
	if f.Deployment != "azure-functions" {
		switch id {
		case "dashboard":
			return []string{"vm", "postgres", "sign-in-app", "runtime-secrets", "runtime-image", "runtime-activation", "snapshot-ingestion"}
		case "postgres":
			if f.Deployment == "single-vm" {
				return []string{"vm", "managed-disk"}
			}
			return []string{"network"}
		case "database-provisioning":
			return []string{"postgres", "runtime-secrets"}
		case "snapshot-ingestion":
			return []string{"vm", "database-provisioning", "runtime-image"}
		}
	}
	result := append([]string{}, contracts[id]...)
	if id == "dashboard" && f.Ingress == "private" {
		result = append(result, "private-connectivity")
	}
	if id == "secrets" && f.Acquisition == "collector" {
		result = append(result, "collection-app")
	}
	if id == "telemetry-export" && f.Acquisition == "collector" {
		result = append(result, "collection")
	}
	return result
}

func componentIDs(f Features) []string {
	selected := map[string]bool{"dashboard": true}
	if f.Acquisition == "collector" {
		selected["collection"] = true
	} else {
		selected["snapshot-ingestion"] = true
	}
	if f.Analytics == "adx" {
		selected["adx"] = true
	}
	if f.Ingress == "private" {
		selected["private-connectivity"] = true
	}
	if f.Telemetry == "otlp" {
		selected["telemetry-export"] = true
	}
	if f.SessionDataLake {
		selected["session-data-lake"] = true
	}
	if f.WebhookGateway {
		selected["webhook-gateway"] = true
	}
	var include func(string)
	include = func(id string) {
		for _, dependency := range dependencies(id, f) {
			if !selected[dependency] {
				selected[dependency] = true
				include(dependency)
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(selected)) {
		include(id)
	}
	return slices.Sorted(maps.Keys(selected))
}

func dependencyOrder(nodes []Node) ([]string, error) {
	byID := map[string]Node{}
	for _, node := range nodes {
		if node.ID == "" {
			return nil, fmt.Errorf("empty topology node ID")
		}
		if _, exists := byID[node.ID]; exists {
			return nil, fmt.Errorf("duplicate topology node: %s", node.ID)
		}
		byID[node.ID] = node
	}
	names := slices.Sorted(maps.Keys(byID))
	directed := simple.NewDirectedGraph()
	ids := map[string]int64{}
	for index, name := range names {
		id := int64(index)
		ids[name] = id
		directed.AddNode(simple.Node(id))
	}
	for _, node := range nodes {
		for _, dependency := range node.DependsOn {
			id, exists := ids[dependency]
			if !exists {
				return nil, fmt.Errorf("missing dependency: %s", dependency)
			}
			if id == ids[node.ID] {
				return nil, fmt.Errorf("dependency cycle at %s", node.ID)
			}
			directed.SetEdge(directed.NewEdge(simple.Node(id), simple.Node(ids[node.ID])))
		}
	}
	ordered, err := topo.SortStabilized(directed, func(nodes []graph.Node) {
		slices.SortFunc(nodes, func(a, b graph.Node) int {
			if a.ID() < b.ID() {
				return -1
			}
			if a.ID() > b.ID() {
				return 1
			}
			return 0
		})
	})
	if err != nil {
		return nil, fmt.Errorf("dependency cycle: %w", err)
	}
	order := make([]string, len(ordered))
	for index, node := range ordered {
		order[index] = names[node.ID()]
	}
	return order, nil
}

func sortedCopy(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return result
}

func CheckTopology(t Topology) error {
	if t.Version != 1 || t.Backend != "azure" {
		return fmt.Errorf("unsupported topology version or backend")
	}
	if err := t.Features.validate(); err != nil {
		return err
	}
	order, err := dependencyOrder(t.Nodes)
	if err != nil {
		return err
	}
	ids := []string{}
	for _, node := range t.Nodes {
		ids = append(ids, node.ID)
	}
	if !slices.Equal(sortedCopy(ids), componentIDs(t.Features)) {
		return fmt.Errorf("topology component set does not satisfy selected features")
	}
	for _, node := range t.Nodes {
		kind := "managed"
		if external[node.ID] {
			kind = "external"
		}
		if node.Kind != kind {
			return fmt.Errorf("incorrect dependency kind for %s", node.ID)
		}
		if !slices.Equal(sortedCopy(node.DependsOn), sortedCopy(dependencies(node.ID, t.Features))) {
			return fmt.Errorf("misconfigured dependencies for %s", node.ID)
		}
	}
	if !slices.Equal(order, t.Order) {
		return fmt.Errorf("topology dependency order is incorrect")
	}
	return nil
}
