package cli

import (
	"context"
	"fmt"

	"github.com/github/gh-aw/pkg/logger"
	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

var log = logger.New("cao:cli")

func NewRootCommand(version string) *cobra.Command {
	return newRootCommand(version, ghcli.Version)
}

func newRootCommand(version string, ghVersion func(context.Context) (string, error)) *cobra.Command {
	return newCommand(version, ghVersion, ghcli.Exec, nativeOperator(ghcli.Exec))
}

type ghExecutor func(context.Context, ...string) (string, string, error)
type runtimeExecutor func(*cobra.Command, []string) error

func newCommand(version string, ghVersion func(context.Context) (string, error), gh ghExecutor, runtime runtimeExecutor) *cobra.Command {
	root := &cobra.Command{
		Use:   "cao",
		Short: "Central Agentic Ops GitHub CLI extension",
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "gh cao",
		},
		Long: `Configure and operate Central Agentic Ops from a control repository.

All operator commands run natively in Go; Node.js is not required.
The binary embeds the reviewed dashboard catalog and reads canonical SQLite snapshots.
Activity and Dashboard Actions invoke node activity/cao.mjs directly.
Help, version, and catalog discovery work offline.`,
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			log.Printf("Executing command: %s", cmd.Name())
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("gh cao version {{.Version}}\n")
	root.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the extension version",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "gh cao version %s\n", version)
				return err
			},
		},
		doctorCommand(ghVersion, gh),
	)
	addPolicyCommands(root, gh)
	root.AddCommand(addCommand(gh))
	root.AddCommand(admissionCommands(gh))
	root.AddCommand(repositoryCommands(gh))
	addOperatorCommands(root, runtime, gh)
	root.AddCommand(deployCommand(defaultDeployAzureDeps()))
	root.AddCommand(evidenceCommand(defaultEvidenceDeps(version)))
	root.AddCommand(dashboardCommand(version, boundedDashboardGH, embeddedDashboardLogs))
	root.AddCommand(dashboardActionCommand())
	return root
}
