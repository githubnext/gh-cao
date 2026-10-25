package main

import (
	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
)

func newADXCommand() *cobra.Command {
	command := &cobra.Command{Use: "adx", Short: "Optional ADX destination setup"}
	command.AddCommand(&cobra.Command{
		Use: "schema", Short: "Print operator-applied ADX Free-compatible KQL schema and functions",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write([]byte(adxingest.Schema()))
			return err
		},
	})
	return command
}
