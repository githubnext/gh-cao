package server

import (
	"context"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

const valueCollectionCostsSourceName = "value-collection-costs"

func (a *App) valueCollectionCostsSource(ctx context.Context, allowed bool) (model.Source, error) {
	if !allowed {
		return unavailableSource(valueCollectionCostsSourceName), nil
	}
	store, ok := a.database.(*postgresx.Store)
	if !ok || store == nil {
		return unavailableSource(valueCollectionCostsSourceName), nil
	}
	return store.ValueCollectionCostSource(ctx, time.Now().UTC().AddDate(0, 0, -30), 100000)
}
