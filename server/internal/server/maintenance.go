package server

import (
	"context"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

const operationalMaintenanceInterval = 30 * time.Second
const operationalMaintenanceTimeout = 10 * time.Second

func (a *App) maintainOperationalCaches(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, operationalMaintenanceTimeout)
	defer cancel()
	err := a.services.Backend.Maintain(ctx)
	var pressure *operational.MaintenanceError
	if errors.As(err, &pressure) {
		a.config.Logger.Printf("Operational cache maintenance pressure used_bytes=%d budget_bytes=%d disposable_cache_entries_evicted=%d action=inspect_operational_backend_capacity_and_preserve_protected_state",
			pressure.UsedBytes, pressure.BudgetBytes, pressure.Evicted)
	}
	if err == nil {
		health, healthErr := a.services.Health.Health(ctx)
		if healthErr != nil {
			return healthErr
		}
		if health.CacheDisabled {
			a.config.Logger.Printf("Operational disposable caching disabled; protected storage remains enabled")
		}
	}
	return err
}

func (a *App) runOperationalMaintenance(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.maintainOperationalCaches(ctx); err != nil && ctx.Err() == nil {
				// Do not log raw backend errors, which can include internal keys.
				a.config.Logger.Printf("Operational cache maintenance failed")
			}
		}
	}
}

func (a *App) initializeOperationalMaintenance(ctx context.Context) error {
	if a.services.Backend == nil {
		return nil
	}
	err := a.maintainOperationalCaches(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var pressure *operational.MaintenanceError
		if errors.As(err, &pressure) {
			return err
		}
		return errors.New("operational cache maintenance is unavailable")
	}
	return nil
}
