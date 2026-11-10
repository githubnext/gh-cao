package server

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/marketplace"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var marketplaceLog = logger.New("cao:marketplace")

// marketplacePolicyPathEnv overrides the CAO policy path marketplace
// registries are read from. It is intentionally configurable per spec, since
// the hosted backend and a local checkout may keep the control-plane policy
// at different relative locations.
const marketplacePolicyPathEnv = "CAO_MARKETPLACE_POLICY_PATH"

// defaultMarketplacePolicyPath matches where cao-created control repositories
// keep their policy: .github/workflows/cao.json.
const defaultMarketplacePolicyPath = ".github/workflows/cao.json"

func marketplacePolicyPath() string {
	if value := strings.TrimSpace(os.Getenv(marketplacePolicyPathEnv)); value != "" {
		return value
	}
	return defaultMarketplacePolicyPath
}

// storeMarketplaceCache adapts operational.Cache to marketplace.Cache so resolved
// registries are cached in Redis, isolated by registry id and by the active
// dashboard data revision (generation).
type storeMarketplaceCache struct {
	store operational.Cache
}

func (c storeMarketplaceCache) Get(ctx context.Context, registryID, generation string) ([]byte, bool, error) {
	data, err := c.store.CachedMarketplaceRegistry(ctx, registryID, generation)
	if err != nil {
		return nil, false, err
	}
	return data, data != nil, nil
}

func (c storeMarketplaceCache) Set(ctx context.Context, registryID, generation string, data []byte, ttl time.Duration) error {
	return c.store.CacheMarketplaceRegistry(ctx, registryID, generation, data, ttl)
}

// marketplaceSource resolves the read-only campaign package catalog for
// injection as an ordinary dashboard source. It never returns an error: a
// missing or invalid policy degrades to an empty, "unavailable" source rather
// than failing the whole query, matching how every other source responds to
// missing data.
func marketplaceSource(ctx context.Context, store operational.Cache, generation string) model.Source {
	config, err := marketplace.LoadConfigFile(marketplacePolicyPath())
	if err != nil {
		marketplaceLog.PrintfContext(ctx, "marketplace policy unavailable")
		return marketplaceUnavailableSource()
	}
	result := marketplace.Resolve(ctx, config, generation, storeMarketplaceCache{store: store}, marketplace.Options{})
	rows := make([]model.Row, 0, len(result.Packages))
	for _, pkg := range result.Packages {
		rows = append(rows, pkg.Row())
	}
	diagnostics := make([]model.Row, 0, len(result.Diagnostics))
	for _, diagnostic := range result.Diagnostics {
		diagnostics = append(diagnostics, diagnostic.Row())
	}
	marketplaceLog.PrintfContext(ctx, "resolved marketplace catalog packages=%d registries=%d", len(rows), len(result.Diagnostics))
	return model.Source{
		Source: marketplace.SourceName,
		Rows:   rows,
		Metadata: model.Metadata{
			"source-id":            marketplace.SourceName,
			"availability":         marketplaceAvailability(rows),
			"registry-diagnostics": diagnostics,
		},
	}
}

func marketplaceUnavailableSource() model.Source {
	return model.Source{
		Source: marketplace.SourceName,
		Rows:   []model.Row{},
		Metadata: model.Metadata{
			"source-id":    marketplace.SourceName,
			"availability": "unavailable",
			"completeness": "unknown",
			"freshness":    "unknown",
		},
	}
}

func marketplaceAvailability(rows []model.Row) string {
	if len(rows) == 0 {
		return "empty"
	}
	return "available"
}
