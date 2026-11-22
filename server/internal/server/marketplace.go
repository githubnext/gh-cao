package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/marketplace"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/query"
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

func marketplaceOnlyQuery(definitions []query.Definition, requested []string) bool {
	marketplaceRead, otherRead := false, false
	err := query.WalkPlan(definitions, requested, func(name string) error {
		if name == "$marketplacePackages" {
			marketplaceRead = true
		} else {
			otherRead = true
		}
		return nil
	}, func(query.Definition) error { return nil })
	return err == nil && marketplaceRead && !otherRead
}

// storeMarketplaceCache adapts operational.Cache to marketplace.Cache so resolved
// registries are isolated by registry id and dashboard generation, including
// the explicit pre-snapshot generation.
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
	return marketplaceResultSource(ctx, result)
}

func (a *App) localMarketplaceSource(ctx context.Context) model.Source {
	connection := a.operatorConnection()
	if !validOperatorAdapterURL(connection.AdapterURL) {
		marketplaceLog.PrintfContext(ctx, "local marketplace adapter unavailable")
		return marketplaceUnavailableSource()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, connection.AdapterURL+"/marketplace", nil)
	if err != nil {
		marketplaceLog.PrintfContext(ctx, "local marketplace request unavailable")
		return marketplaceUnavailableSource()
	}
	// Match the other operator-adapter calls: no ambient proxy and no
	// redirects, so the private adapter cannot steer this request elsewhere.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		marketplaceLog.PrintfContext(ctx, "local marketplace resolution failed")
		return marketplaceUnavailableSource()
	}
	defer func() { _ = response.Body.Close() }()
	var result marketplace.Result
	decoder := json.NewDecoder(io.LimitReader(response.Body, (32<<20)+1))
	if response.StatusCode != http.StatusOK || decoder.Decode(&result) != nil {
		marketplaceLog.PrintfContext(ctx, "local marketplace resolution failed")
		return marketplaceUnavailableSource()
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		marketplaceLog.PrintfContext(ctx, "local marketplace response invalid")
		return marketplaceUnavailableSource()
	}
	return marketplaceResultSource(ctx, &result)
}

func marketplaceResultSource(ctx context.Context, result *marketplace.Result) model.Source {
	rows := make([]model.Row, 0, len(result.Packages))
	for _, pkg := range result.Packages {
		rows = append(rows, pkg.Row())
	}
	diagnostics := make([]model.Row, 0, len(result.Diagnostics))
	available := len(result.Diagnostics) == 0
	completeness := "complete"
	for _, diagnostic := range result.Diagnostics {
		diagnostics = append(diagnostics, diagnostic.Row())
		if diagnostic.Status == "available" {
			available = true
		} else {
			completeness = "partial"
		}
	}
	availability, freshness := marketplaceAvailability(rows), "fresh"
	if !available {
		availability, completeness, freshness = "unavailable", "unknown", "unknown"
	}
	marketplaceLog.PrintfContext(ctx, "resolved marketplace catalog packages=%d registries=%d", len(rows), len(result.Diagnostics))
	return model.Source{
		Source: marketplace.SourceName,
		Rows:   rows,
		Metadata: model.Metadata{
			"source-id":            marketplace.SourceName,
			"availability":         availability,
			"completeness":         completeness,
			"freshness":            freshness,
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
