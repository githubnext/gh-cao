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

func TestServeResourcePathsSelectEmbeddedOrDisk(t *testing.T) {
	embedded := serveResourcePaths("")
	disk := serveResourcePaths(filepath.Join(t.TempDir(), "site"))
	if len(embedded) != 5 || len(disk) != 5 {
		t.Fatalf("unexpected resource count: embedded=%d disk=%d", len(embedded), len(disk))
	}
	for flag, path := range embedded {
		if path == disk[flag] {
			t.Fatalf("%s: embedded and disk selections must differ: %q", flag, path)
		}
		if filepath.IsAbs(path) {
			t.Fatalf("%s: embedded selection must not be a disk path: %q", flag, path)
		}
	}
	if got := filepath.Base(disk["cli-actions-catalog"]); got != "dashboard.json" {
		t.Fatalf("cli-actions-catalog resolved to %q", got)
	}
}

func TestResolveServeResourcesKeepsExplicitAndUndeclaredFlags(t *testing.T) {
	directory := t.TempDir()
	cmd := &cobra.Command{Use: "partial"}
	cmd.Flags().String("agent-catalog", "", "")
	cmd.Flags().String("cli-actions-catalog", "", "")
	if err := cmd.ParseFlags([]string{"--cli-actions-catalog=operator.json"}); err != nil {
		t.Fatal(err)
	}
	resolveServeResources(cmd, directory)
	if got, _ := cmd.Flags().GetString("cli-actions-catalog"); got != "operator.json" {
		t.Fatalf("explicit flag was overwritten: %q", got)
	}
	want := dashboardassets.Path(directory, dashboardassets.AgentCatalog)
	if got, _ := cmd.Flags().GetString("agent-catalog"); got != want {
		t.Fatalf("agent-catalog = %q, want %q", got, want)
	}
	if cmd.Flags().Lookup("mcp-contract") != nil {
		t.Fatal("undeclared flags must not be created")
	}
}
