package main

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/server/internal/marketplace"
)

func newMarketplaceResolveCommand() *cobra.Command {
	var policy string
	cmd := &cobra.Command{
		Use: "__marketplace", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, err := marketplace.LoadConfigFile(policy)
			if err != nil {
				return err
			}
			result := marketplace.Resolve(cmd.Context(), config, "", nil, marketplace.Options{})
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	cmd.Flags().StringVar(&policy, "policy", "", "Exact local CAO policy")
	_ = cmd.MarkFlagRequired("policy")
	return cmd
}
