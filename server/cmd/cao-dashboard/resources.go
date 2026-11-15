package main

import (
	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
)

var resourcesLog = debuglogger.New("cao:dashboard:resources")

// serveResourcePaths names the default resource path for each serve flag:
// embedded when directory is empty, otherwise inside the selected directory.
func serveResourcePaths(directory string) map[string]string {
	return map[string]string{
		"database-queries":    dashboardassets.Path(directory, dashboardassets.DatabaseQueries),
		"dashboard-queries":   dashboardassets.Path(directory, dashboardassets.DashboardQueries),
		"agent-catalog":       dashboardassets.Path(directory, dashboardassets.AgentCatalog),
		"mcp-contract":        dashboardassets.Path(directory, dashboardassets.MCPContract),
		"cli-actions-catalog": dashboardassets.Path(directory, "dashboard.json"),
	}
}

// resolveServeResources fills each declared resource flag the operator left
// unset; explicit values are never overwritten.
func resolveServeResources(cmd *cobra.Command, directory string) {
	defaulted := 0
	for flag, path := range serveResourcePaths(directory) {
		if value := cmd.Flags().Lookup(flag); value != nil && !value.Changed {
			_ = value.Value.Set(path)
			defaulted++
		}
	}
	resourcesLog.Printf("resolved serve resources: embedded=%t defaulted=%d", directory == "", defaulted)
}
