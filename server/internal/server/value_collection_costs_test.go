package server

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestValueCollectionCostsRequiresAdministratorAndIsRegistered(t *testing.T) {
	app := &App{}
	source, err := app.valueCollectionCostsSource(t.Context(), false)
	if err != nil || source.Metadata["availability"] != "unavailable" || len(source.Rows) != 0 {
		t.Fatalf("unauthorized Value costs were exposed: %+v, %v", source, err)
	}
	if !slices.Contains(RuntimeQuerySourceNames(), valueCollectionCostsSourceName) {
		t.Fatal("Value collection costs are not registered as a runtime query source")
	}
}

func TestValueCollectionCostQueriesBypassResultCacheTransitively(t *testing.T) {
	for _, name := range []string{valueCollectionCostsSourceName, "derived", "joined", "union"} {
		t.Run(name, func(t *testing.T) {
			cache := &queryCacheClient{}
			database := &webhookQueryProbe{}
			app := &App{database: database, services: redisx.NewStore(cache, "value-cost-cache").OperationalServices()}
			app.config.DashboardQueries = []query.Definition{
				{Name: "derived", From: valueCollectionCostsSourceName},
				{Name: "joined", From: "$runs", Joins: []query.Join{{
					Source: "derived", On: []query.JoinKey{{Left: "id", Right: "id"}},
				}}},
				{Name: "union", From: "$runs", Union: []string{"derived"}},
			}
			_, _, err := app.executeQuery(t.Context(), queryRequest{SourceNames: []string{name}}, true)
			if !errors.Is(err, context.Canceled) || database.reads != 1 || cache.reads != 0 || cache.writes != 0 {
				t.Fatalf("live query accessed cached state: database=%d cache reads=%d writes=%d error=%v",
					database.reads, cache.reads, cache.writes, err)
			}
		})
	}
}
