package main

import (
	"context"
	"encoding/json"

	"github.com/spf13/cobra"

	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/marketplace"
)

var marketplaceLog = debuglogger.New("cao:dashboard:marketplace")

// resolveMarketplacePolicy loads the exact local policy and resolves its
// registries once, without a cache.
func resolveMarketplacePolicy(ctx context.Context, policy string) (*marketplace.Result, error) {
	config, err := marketplace.LoadConfigFile(policy)
	if err != nil {
		marketplaceLog.Printf("marketplace policy rejected: reason=load_failed")
		return nil, err
	}
	result := marketplace.Resolve(ctx, config, "", nil, marketplace.Options{})
	marketplaceLog.Printf("marketplace resolved: registries=%d packages=%d diagnostics=%d", len(config.Registries), len(result.Packages), len(result.Diagnostics))
	return result, nil
}

func newMarketplaceResolveCommand() *cobra.Command {
	var policy string
	cmd := &cobra.Command{
		Use: "__marketplace", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := resolveMarketplacePolicy(cmd.Context(), policy)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	cmd.Flags().StringVar(&policy, "policy", "", "Exact local CAO policy")
	_ = cmd.MarkFlagRequired("policy")
	return cmd
}
