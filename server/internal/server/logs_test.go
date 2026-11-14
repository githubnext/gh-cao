package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

type logCounterClient struct {
	fields   []any
	err      error
	queueErr error
}

func (client logCounterClient) Do(_ context.Context, command ...string) (any, error) {
	if client.err != nil {
		return nil, client.err
	}
	switch command[0] {
	case "HGETALL":
		return client.fields, nil
	case "XINFO":
		if client.queueErr != nil {
			return nil, client.queueErr
		}
		return []any{[]any{"name", "collectors", "lag", int64(2)}}, nil
	case "ZCARD":
		return int64(1), nil
	case "XPENDING":
		if len(command) > 3 {
			return []any{[]any{"1-0", "collector", int64(0), int64(1)}}, nil
		}
		return []any{int64(4)}, nil
	case "XLEN":
		return int64(5), nil
	default:
		return nil, errors.New("unexpected Redis command")
	}
}

func (logCounterClient) DoMany(context.Context, [][]string) ([]any, error) {
	return nil, errors.New("unexpected Redis commands")
}

func TestServerLogsAuthorization(t *testing.T) {
	app := &App{logs: logger.EnableBuffer(), config: Config{AdminUsers: []string{"operator"}}, oauth: &githubOAuth{}}
	for _, test := range []struct {
		name   string
		ctx    context.Context
		status int
	}{
		{"no session", context.Background(), http.StatusForbidden},
		{"non admin", context.WithValue(context.Background(), oauthSessionContextKey{}, oauthSession{Login: "viewer"}), http.StatusForbidden},
		{"admin", context.WithValue(context.Background(), oauthSessionContextKey{}, assuredSession("operator")), http.StatusOK},
		{"actions", context.WithValue(context.Background(), githubActionsActorContextKey{}, "workflow"), http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(test.ctx, http.MethodGet, "/api/admin/logs", nil)
			response := httptest.NewRecorder()
			app.serverLogs(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if test.status == http.StatusOK {
				if got := response.Header().Get("Content-Type"); got != "application/json" {
					t.Fatalf("Content-Type = %q", got)
				}
				if got := response.Header().Get("Content-Disposition"); got != `attachment; filename="cao-server-logs.json"` {
					t.Fatalf("Content-Disposition = %q", got)
				}
				if got := response.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("Cache-Control = %q", got)
				}
				var snapshot struct {
					Logs        []json.RawMessage `json:"logs"`
					Operational struct {
						Status string `json:"status"`
					} `json:"operational"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil ||
					snapshot.Logs == nil || snapshot.Operational.Status != "not-configured" {
					t.Fatalf("invalid log snapshot: %s (%v)", response.Body.String(), err)
				}
			}
		})
	}
}

func TestServerLogsOnlyExportsSafeRedisCounters(t *testing.T) {
	for _, test := range []struct {
		name       string
		client     logCounterClient
		collection bool
		status     string
	}{
		{"available", logCounterClient{fields: []any{
			"taskQueued", "3", "lastFailureCode", "redis", "unexpected", "private-value",
		}}, false, "ok"},
		{"collection queue", logCounterClient{fields: []any{"taskQueued", "3"}}, true, "ok"},
		{"collection queue unavailable", logCounterClient{
			fields: []any{"taskQueued", "3"}, queueErr: errors.New("private queue detail"),
		}, true, "unavailable"},
		{"unavailable", logCounterClient{err: errors.New("private Redis connection detail")}, false, "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := redisx.NewStore(test.client, "log-test")
			app := &App{
				logs: logger.EnableBuffer(), services: store.OperationalServices(),
			}
			if test.collection {
				app.reconciler = &Collector{reporter: collect.Reporter{Queue: collect.Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}}}
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/admin/logs", nil)
			response := httptest.NewRecorder()
			app.serverLogs(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var snapshot serverLogSnapshot
			if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Logs == nil || snapshot.Operational.Status != test.status {
				t.Fatalf("unexpected snapshot: %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private") ||
				strings.Contains(response.Body.String(), "lastFailureCode") {
				t.Fatalf("internal Redis data leaked: %s", response.Body.String())
			}
			if test.status == "ok" && (len(snapshot.Operational.Counters) != 1 ||
				snapshot.Operational.Counters["taskQueued"] != 3) {
				t.Fatalf("unexpected counters: %s", response.Body.String())
			}
			if test.status == "unavailable" && snapshot.Operational.Counters != nil {
				t.Fatalf("exposed counters on failure: %s", response.Body.String())
			}
			if test.collection && test.status == "ok" && (snapshot.Operational.QueueDepth == nil || *snapshot.Operational.QueueDepth != 3 ||
				snapshot.Operational.PendingTasks == nil || *snapshot.Operational.PendingTasks != 4 ||
				snapshot.Operational.DeadLetters == nil || *snapshot.Operational.DeadLetters != 5) {
				t.Fatalf("unexpected collection queue counts: %s", response.Body.String())
			}
			if !test.collection && (snapshot.Operational.QueueDepth != nil ||
				snapshot.Operational.PendingTasks != nil || snapshot.Operational.DeadLetters != nil) {
				t.Fatalf("collection queue counts should be absent: %s", response.Body.String())
			}
		})
	}
}

func TestServerLogsRouteRequiresAuthenticationAndOptIn(t *testing.T) {
	const token = "local-dashboard-access-token-with-32-characters"
	const actionsToken = "local-actions-access-token-with-32-characters"
	for _, test := range []struct {
		name    string
		bearer  string
		actor   string
		enabled bool
		status  int
	}{
		{"anonymous", "", "", true, http.StatusUnauthorized},
		{"invalid token", "invalid", "", true, http.StatusUnauthorized},
		{"local administrator", token, "", true, http.StatusOK},
		{"actions without actor", actionsToken, "", true, http.StatusUnauthorized},
		{"actions actor", actionsToken, "workflow", true, http.StatusOK},
		{"disabled route", token, "", false, http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := &App{accessToken: token, actionsToken: actionsToken, actionsActor: "workflow"}
			if test.enabled {
				current.logs = logger.EnableBuffer()
			}
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost/api/admin/logs", nil)
			if test.bearer != "" {
				request.Header.Set("Authorization", "Bearer "+test.bearer)
			}
			if test.actor != "" {
				request.Header.Set("X-GitHub-Actor", test.actor)
			}
			response := httptest.NewRecorder()
			handler := http.HandlerFunc(current.serverLogs)
			if !test.enabled {
				handler = http.NotFound
			}
			current.requireAccess(handler).ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestCollectionQueueCounts(t *testing.T) {
	for _, test := range []struct {
		name        string
		client      logCounterClient
		wantDepth   int64
		wantPending int64
		wantDead    int64
		wantErr     bool
	}{
		{"available", logCounterClient{}, 3, 4, 5, false},
		{"unavailable", logCounterClient{queueErr: errors.New("private queue detail")}, 0, 0, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := redisx.NewStore(test.client, "queue-test")
			queue := collect.Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
			depth, pending, dead, err := collectionQueueCounts(t.Context(), queue)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if depth != test.wantDepth || pending != test.wantPending || dead != test.wantDead {
				t.Fatalf("counts = (%d, %d, %d), want (%d, %d, %d)",
					depth, pending, dead, test.wantDepth, test.wantPending, test.wantDead)
			}
		})
	}
}
