package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func (a *App) refreshInventory(response http.ResponseWriter, request *http.Request) {
	if !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "administrator access is required")
		return
	}
	collector := a.Collector()
	if collector == nil {
		a.startIngestion(response, request)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	queued, err := collector.requestInventoryRefresh(ctx)
	if err != nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable,
			fmt.Sprintf("inventory refresh admission is incomplete; %d already queued tasks may still run", queued))
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{
		"requested": true, "mode": "inventory", "queued": queued,
	})
}

// Scan only durable enrollment. Admission-only servers need neither App
// credentials nor an inventory provider to request this existing worker lane.
func (c *Collector) requestInventoryRefresh(ctx context.Context) (int, error) {
	authority := c.enrollment.Authority
	if authority == nil {
		return 0, collectnative.ErrUnsupported
	}
	installations, err := authority.Installations(ctx)
	if err != nil {
		return 0, err
	}
	if len(installations) > 100000 {
		return 0, collectnative.ErrCapacity
	}
	limit := 100000
	if c.config.InventoryLimit > 0 {
		limit = min(limit, c.config.InventoryLimit)
	}
	scopes := make([]collectnative.EnrollmentScope, 0)
	for _, installation := range installations {
		for cursor := ""; ; {
			names, next, err := authority.ScanRepositories(ctx, installation, cursor, 100)
			if err != nil {
				return 0, err
			}
			for _, name := range names {
				scope, err := authority.CurrentScope(ctx, name)
				if errors.Is(err, collectnative.ErrIdentity) {
					continue
				}
				if err != nil {
					return 0, err
				}
				if scope.InstallationID != installation {
					continue
				}
				scopes = append(scopes, scope)
				if len(scopes) > limit {
					return 0, collectnative.ErrCapacity
				}
			}
			if next == "" {
				break
			}
			if next <= cursor {
				return 0, collectnative.ErrIdentity
			}
			cursor = next
		}
	}
	queued := 0
	for _, scope := range scopes {
		result, err := c.queue.RequestInventoryRefresh(ctx, scope)
		if err != nil {
			return queued, err
		}
		if result.Enqueued {
			queued++
		}
	}
	return queued, nil
}
