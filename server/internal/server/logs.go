package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/logger"
)

var serverLogsLog = logger.New("cao:server:logs")

type serverLogOperational struct {
	Status       string           `json:"status"`
	Counters     map[string]int64 `json:"counters,omitempty"`
	QueueDepth   *int64           `json:"queueDepth,omitempty"`
	PendingTasks *int64           `json:"pendingTasks,omitempty"`
	DeadLetters  *int64           `json:"deadLetters,omitempty"`
}

type serverLogSnapshot struct {
	Logs        []json.RawMessage    `json:"logs"`
	Operational serverLogOperational `json:"operational"`
}

// collectionQueueCounts reads the collection queue's depth, pending, and
// dead-letter counts as one unit. It is a pure extraction from
// serverLogOperational's inline error handling, so the "any count unavailable
// fails the whole snapshot" rule is testable directly against a Queue
// without going through the HTTP handler.
func collectionQueueCounts(ctx context.Context, queue collect.Queue) (depth, pending, dead int64, err error) {
	if depth, err = queue.Depth(ctx); err != nil {
		return 0, 0, 0, err
	}
	if pending, err = queue.Pending(ctx); err != nil {
		return 0, 0, 0, err
	}
	if dead, err = queue.DeadLetters(ctx); err != nil {
		return 0, 0, 0, err
	}
	return depth, pending, dead, nil
}

func (a *App) serverLogOperational(ctx context.Context) serverLogOperational {
	if a.services.IngestionMetrics == nil {
		return serverLogOperational{Status: "not-configured"}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	counters, _, err := a.services.IngestionMetrics.IngestionHealth(ctx)
	if err != nil {
		serverLogsLog.PrintfContext(ctx, "server log snapshot unavailable stage=ingestion-health")
		return serverLogOperational{Status: "unavailable"}
	}
	result := serverLogOperational{Status: "ok", Counters: counters}
	if collector := a.Collector(); collector != nil {
		depth, pending, dead, err := collectionQueueCounts(ctx, collector.reporter.Queue)
		if err != nil {
			serverLogsLog.PrintfContext(ctx, "server log snapshot unavailable stage=collection-queue")
			return serverLogOperational{Status: "unavailable"}
		}
		result.QueueDepth, result.PendingTasks, result.DeadLetters = &depth, &pending, &dead
	}
	return result
}

func (a *App) serverLogsAuthorized(ctx context.Context) bool {
	actor, actions := ctx.Value(githubActionsActorContextKey{}).(string)
	return (actions && actor != "") || a.adminAuthorizedContext(ctx)
}

func (a *App) serverLogsSnapshot(ctx context.Context) serverLogSnapshot {
	return serverLogSnapshot{Logs: a.logs.Snapshot(), Operational: a.serverLogOperational(ctx)}
}

func (a *App) serverLogs(response http.ResponseWriter, request *http.Request) {
	if !a.serverLogsAuthorized(request.Context()) {
		writeError(request.Context(), response, http.StatusForbidden, "administrator access is required")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Content-Disposition", `attachment; filename="cao-server-logs.json"`)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(response).Encode(a.serverLogsSnapshot(request.Context()))
}
