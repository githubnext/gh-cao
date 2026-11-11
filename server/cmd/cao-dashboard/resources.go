package main

import (
	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
)

func resolveServeResources(cmd *cobra.Command, directory string) {
	for flag, name := range map[string]string{
		"database-queries":    dashboardassets.DatabaseQueries,
		"dashboard-queries":   dashboardassets.DashboardQueries,
		"agent-catalog":       dashboardassets.AgentCatalog,
		"mcp-contract":        dashboardassets.MCPContract,
		"cli-actions-catalog": "dashboard.json",
	} {
		if value := cmd.Flags().Lookup(flag); value != nil && !value.Changed {
			_ = value.Value.Set(dashboardassets.Path(directory, name))
		}
	}
}
