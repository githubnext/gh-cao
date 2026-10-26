package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
)

var adxLog = debuglogger.New("cao:dashboard:adx")

// traceSettings resolves trace configuration and the destination namespace
// from the environment accessor, failing closed unless both are present.
func traceSettings(get func(string) string) (adxtrace.Config, string, error) {
	config, err := adxtrace.FromEnv(get)
	if err != nil {
		adxLog.Printf("trace configuration rejected")
		return config, "", err
	}
	namespace, err := traceNamespace(config, get("CAO_ADX_ENDPOINT"), get("CAO_ADX_DATABASE"))
	return config, namespace, err
}

// traceNamespace requires enabled traces and a complete destination identity.
func traceNamespace(config adxtrace.Config, endpoint, database string) (string, error) {
	if !config.Enabled || endpoint == "" || database == "" {
		adxLog.Printf("trace settings incomplete: enabled=%t endpoint=%t database=%t", config.Enabled, endpoint != "", database != "")
		return "", errors.New("trace configuration and destination identity are required")
	}
	adxLog.Printf("trace settings resolved: mining=%t durable=%t", config.MiningEnabled, config.DurableStorage)
	return adxtrace.Namespace(endpoint, database), nil
}

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
	settings := func() (adxtrace.Config, string, error) { return traceSettings(os.Getenv) }
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
