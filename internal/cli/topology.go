package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/topology"
)

func topologyCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "topology", Short: "Solve and check review-only TOSCA deployment topologies",
		Long: "Generate Azure Bicep, Mermaid manifests and configuration instructions from feature choices.\nUses the embedded Puccini TOSCA 1.3 engine in Go; no Node.js, cloud calls or deployment.\nGeneration requires Microsoft's official Bicep CLI on PATH and validates every emitted template.",
		Args: cobra.NoArgs,
	}
	init := &cobra.Command{
		Use: "init", Short: "Write an example feature configuration without overwriting files", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			destination, _ := cmd.Flags().GetString("out")
			deployment, _ := cmd.Flags().GetString("deployment")
			if err := topology.InitExample(destination, deployment); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Wrote example requirements to %s; replace example identities before use.\n", destination)
			return err
		},
	}
	init.Flags().String("out", "requirements.yaml", "New YAML requirements file")
	init.Flags().String("deployment", "azure-functions", "Example: single-vm, minimal-azure, azure-functions, azure-container-apps, or full-azure")
	generate := &cobra.Command{
		Use: "generate", Short: "Resolve requirements and write a new deployment folder", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			source, _ := cmd.Flags().GetString("config")
			destination, _ := cmd.Flags().GetString("out")
			if destination == "" {
				return fmt.Errorf("topology generate requires --out")
			}
			content, err := topology.ReadRequirements(source)
			if err != nil {
				return err
			}
			directory, err := filepath.Abs(destination)
			if err != nil {
				return err
			}
			result, err := topology.Generate(cmd.Context(), content, directory, topology.BicepCLI{Diagnostics: cmd.ErrOrStderr()})
			if err != nil {
				return err
			}
			return topologyReport(cmd, "Generated", directory, result)
		},
	}
	generate.Flags().String("config", "requirements.yaml", "Reviewed YAML feature choices and non-secret parameters")
	generate.Flags().String("out", "", "New private deployment folder (must not exist)")
	check := &cobra.Command{
		Use: "check DIRECTORY", Short: "Check architecture rules, TOSCA dependencies and bundle drift", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			directory, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			result, err := topology.Check(cmd.Context(), directory)
			if err != nil {
				return err
			}
			return topologyReport(cmd, "Checked", directory, result)
		},
	}
	command.AddCommand(init, generate, check)
	return command
}

func topologyReport(cmd *cobra.Command, verb, directory string, result topology.Topology) error {
	report, err := topology.ReadRuleReport(directory)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s topology (%d components): %s\nArchitecture rules: %d errors, %d warnings; see RULES.md.\nExternal prerequisites require independent evidence; no deployment or App changes performed.\n", verb, result.Backend, result.Features.Deployment, len(result.Nodes), directory, report.Errors, report.Warnings)
	return err
}
