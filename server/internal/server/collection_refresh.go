package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

const localInventoryQueue = "collect:local-inventory"
const localInventoryGroup = "inventory"

func (a *App) localInventoryRefreshConfigured() bool {
	connection := a.operatorConnection()
	_, fenced := a.services.Queue.(operational.FencedTaskQueue)
	return a.oauth == nil && connection != nil && connection.InventoryRefresh &&
		validOperatorAdapterURL(connection.AdapterURL) && fenced
}

func (a *App) inventoryRefreshConfigured() bool {
	collector := a.Collector()
	return a.localInventoryRefreshConfigured() || collector != nil && collector.native != nil
}

func (a *App) collectionRefresh(response http.ResponseWriter, request *http.Request) {
	if !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "administrative authorization is required")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	if !a.inventoryRefreshConfigured() {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "inventory refresh is not configured")
		return
	}
	a.enforceRateLimit(response, request, http.HandlerFunc(a.admitInventoryRefresh),
		requestRatePolicy{name: "inventory-refresh", capacity: 1, window: time.Minute}, a.rateLimitSubject(request))
}

func (a *App) admitInventoryRefresh(response http.ResponseWriter, request *http.Request) {
	var err error
	if a.localInventoryRefreshConfigured() {
		err = a.enqueueLocalInventoryRefresh(request.Context())
	} else {
		var count int
		collector := a.Collector()
		count, err = collector.queue.RequestInventoryRefresh(request.Context(), collector.enrollment)
		if err == nil && count == 0 {
			err = errors.New("no committed inventory scopes")
		}
	}
	if err != nil {
		if errors.Is(err, operational.ErrCapacity) {
			response.Header().Set("Retry-After", "60")
			writeError(request.Context(), response, http.StatusTooManyRequests, "inventory refresh queue is full")
			return
		}
		writeError(request.Context(), response, http.StatusServiceUnavailable, "inventory refresh admission is unavailable; already admitted work remains queued")
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]bool{"queued": true})
}

func (a *App) enqueueLocalInventoryRefresh(ctx context.Context) error {
	_, err := a.services.Queue.EnqueueTask(ctx, operational.EnqueueRequest{
		Queue: localInventoryQueue, Capacity: 8,
		Fields: operational.TaskFields{Task: "inventory-refresh", RecordedAt: time.Now().UTC().Format(time.RFC3339)},
	})
	return err
}

// The owning CLI confirms publication before ACK. A disconnected request keeps
// its receipt recoverable; duplicate rereads are safe and preserve scope.
func (a *App) runLocalInventoryRefresh(ctx context.Context) {
	queue := a.services.Queue.(operational.FencedTaskQueue)
	if err := queue.EnsureQueue(ctx, localInventoryQueue, localInventoryGroup); err != nil {
		serverLog.PrintfContext(ctx, "local inventory queue initialization failed")
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var nextDaily time.Time
	for ctx.Err() == nil {
		if !time.Now().Before(nextDaily) {
			if err := a.enqueueLocalInventoryRefresh(ctx); err == nil {
				nextDaily = time.Now().Add(24 * time.Hour)
			}
		}
		if err := a.stepLocalInventoryRefresh(ctx); err != nil && ctx.Err() == nil {
			serverLog.PrintfContext(ctx, "local inventory refresh deferred")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) stepLocalInventoryRefresh(ctx context.Context) error {
	connection := a.operatorConnection()
	if connection == nil || !connection.InventoryRefresh || !validOperatorAdapterURL(connection.AdapterURL) {
		return errors.New("local inventory adapter is unavailable")
	}
	queue := a.services.Queue.(operational.FencedTaskQueue)
	read := operational.QueueRead{Queue: localInventoryQueue, Group: localInventoryGroup, Consumer: "native-inventory", Count: 1}
	messages, err := queue.ClaimTasks(ctx, read, 20*time.Minute)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		messages, err = queue.ReadTasks(ctx, read)
	}
	if err != nil || len(messages) == 0 {
		return err
	}
	message := messages[0]
	if message.Receipt == "" || message.Fields.Task != "inventory-refresh" {
		return errors.New("invalid inventory refresh receipt")
	}
	attempt, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(attempt, http.MethodPost, connection.AdapterURL+"/inventory/refresh", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("native inventory publication was not confirmed")
	}
	return queue.CompleteTaskFenced(ctx, localInventoryQueue, localInventoryGroup, message.ID, message.Receipt)
}
