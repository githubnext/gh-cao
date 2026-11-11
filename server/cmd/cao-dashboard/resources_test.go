package main

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
)

func TestServeResourceSelection(t *testing.T) {
	for _, command := range []func() *cobra.Command{newServeCommand, newServeHostedCommand} {
		for _, directory := range []string{"", t.TempDir()} {
			cmd := command()
			if err := cmd.ParseFlags([]string{"--site=" + directory, "--mcp-contract=custom-contract.json"}); err != nil {
				t.Fatal(err)
			}
			resolveServeResources(cmd, directory)
			for flag, name := range map[string]string{
				"dashboard-queries": dashboardassets.DashboardQueries,
				"database-queries":  dashboardassets.DatabaseQueries,
				"agent-catalog":     dashboardassets.AgentCatalog,
			} {
				got, err := cmd.Flags().GetString(flag)
				if err != nil || got != dashboardassets.Path(directory, name) {
					t.Fatalf("%s %s: %q, %v", cmd.Name(), flag, got, err)
				}
				if directory != "" && !filepath.IsAbs(got) {
					t.Fatal("disk resources must resolve inside the selected absolute site directory")
				}
			}
			if got, _ := cmd.Flags().GetString("mcp-contract"); got != "custom-contract.json" {
				t.Fatal("resource selection overwrote an explicit path")
			}
		}
	}
}
