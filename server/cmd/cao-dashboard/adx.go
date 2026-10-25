package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func newADXCommand() *cobra.Command {
	command := &cobra.Command{Use: "adx", Short: "Optional ADX destination setup"}
	command.AddCommand(&cobra.Command{
		Use: "schema", Short: "Print operator-applied ADX snapshot and unified trace KQL",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write([]byte(adxingest.Schema() + "\n" + adxtrace.Schema()))
			return err
		},
	})
	command.AddCommand(newTracesCommand())
	return command
}

func newTracesCommand() *cobra.Command {
	command := &cobra.Command{Use: "traces", Short: "Inspect private trace state; no destination calls or training"}
	settings := func() (adxtrace.Config, string, error) {
		config, err := adxtrace.FromEnv(os.Getenv)
		if err != nil {
			return config, "", err
		}
		if !config.Enabled || os.Getenv("CAO_ADX_ENDPOINT") == "" || os.Getenv("CAO_ADX_DATABASE") == "" {
			return config, "", errors.New("trace configuration and destination identity are required")
		}
		return config, adxtrace.Namespace(os.Getenv("CAO_ADX_ENDPOINT"), os.Getenv("CAO_ADX_DATABASE")), nil
	}
	command.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		config, namespace, err := settings()
		if err != nil {
			return err
		}
		status, err := adxtrace.Inspect(cmd.Context(), config, namespace)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
	}})
	command.AddCommand(&cobra.Command{Use: "withdraw owner/repository", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		config, namespace, err := settings()
		if err != nil {
			return err
		}
		if err := adxtrace.Withdraw(cmd.Context(), config, namespace, args[0]); err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write([]byte("Withdrawal persisted; the running trace worker will invalidate destination readiness. Physical purge and external copies require the operator erasure process.\n"))
		return err
	}})
	var spec string
	dataset := &cobra.Command{Use: "dataset", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		config, namespace, err := settings()
		if err != nil {
			return err
		}
		path, err := adxtrace.ExportDatasetFile(cmd.Context(), config, namespace, spec)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write([]byte(path + "\n"))
		return err
	}}
	dataset.Flags().StringVar(&spec, "spec", "", "Private approved dataset specification with labels, tool schemas and split groups")
	command.AddCommand(dataset)
	return command
}
