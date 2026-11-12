package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/redisx"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func backfillServices(t *testing.T) {
	t.Helper()
	if os.Getenv("REDIS_URL") == "" || (os.Getenv("CAO_POSTGRES_URL") == "" && os.Getenv("POSTGRES_URL") == "") {
		if os.Getenv("CAO_BACKFILL_INTEGRATION") == "1" || os.Getenv("CAO_BACKFILL_STRESS") == "1" {
			t.Fatal("explicit backfill integration/stress requires local Postgres and Redis")
		}
		t.Skip("set Postgres/Redis endpoints to run service-backed backfill tests")
	}
}

func syntheticGitHub(t *testing.T, scenario simulator.Scenario) (*githubapp.Client, *simulator.API, *simulator.FaultProxy, func() *githubapp.Client) {
	t.Helper()
	api, err := simulator.NewAPIHandler(scenario, 1)
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(api)
	t.Cleanup(origin.Close)
	proxy, err := simulator.NewFaultProxy(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)
	endpoint := httptest.NewServer(proxy)
	t.Cleanup(endpoint.Close)
	transport, err := simulator.NewLocalTransport(endpoint.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	key, err := stressAppKey()
	if err != nil {
		t.Fatal(err)
	}
	config := githubapp.Config{
		AppID: 1, PrivateKeyPEM: key, BaseURL: endpoint.URL + "/",
		Transport: transport, RequestTimeout: time.Second,
	}
	restart := func() *githubapp.Client {
		client, err := githubapp.New(config)
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	return restart(), api, proxy, restart
}

type syntheticBackfill struct {
	ops      operational.Store
	backfill collect.Backfill
	data     *postgresx.Store
	api      *simulator.API
	proxy    *simulator.FaultProxy
	restart  func() *githubapp.Client
	quota    *githubquota.Service
}

func newSyntheticBackfill(t *testing.T, ctx context.Context, scenario simulator.Scenario) syntheticBackfill {
	t.Helper()
	backfillServices(t)
	client, err := redisx.New(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatal("configure local Redis")
	}
	store := redisx.NewStore(client, fmt.Sprintf("backfill-%d", time.Now().UnixNano()))
	if err := store.Ping(ctx); err != nil {
		t.Fatal("local Redis is unavailable")
	}
	data := testutil.Postgres(t, ctx, "CAO_POSTGRES_URL", "POSTGRES_URL")
	lake := collect.Lake{Directory: t.TempDir()}
	if _, err := scenario.WriteLake(ctx, lake.Directory, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, data, lake.Directory, ingest.Options{DatabaseQueriesPath: backfillDatabaseQueries}); err != nil {
		t.Fatal(err)
	}
	github, api, proxy, restart := syntheticGitHub(t, scenario)
	quota, err := githubquota.New(store, githubquota.Options{SafetyReserve: 1})
	if err != nil {
		t.Fatal(err)
	}
	enrollment := collect.Enrollment{Metadata: store, Leases: store}
	return syntheticBackfill{
		ops: store,
		backfill: collect.Backfill{StateStore: store, Metadata: store, Lake: lake, Enrollment: enrollment, Queue: collect.Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxLength: 5_000_000},
			Enumerator: github, RunEnumerator: github, Quota: quota, QuotaApp: "simulator", WindowDays: 7,
		},
		data: data, api: api, proxy: proxy, restart: restart, quota: quota,
	}
}

func taskQueueLength(ctx context.Context, store operational.TaskQueue, queue string) (int64, error) {
	stats, err := store.QueueStats(ctx, queue, "")
	return stats.Length, err
}
