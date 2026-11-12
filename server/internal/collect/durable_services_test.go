package collect

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func durableErasureServices(t *testing.T, backend string) (operational.OperationalServices, func() operational.OperationalServices) {
	t.Helper()
	ctx := t.Context()
	namespace := fmt.Sprintf("erasure-%d", time.Now().UnixNano())
	if backend == "redis" {
		endpoint := os.Getenv("REDIS_URL")
		if endpoint == "" {
			t.Skip("REDIS_URL is required for durable erasure recovery tests")
		}
		reopen := func() operational.OperationalServices {
			client, err := redisx.New(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			reopened := redisx.NewStore(client, namespace)
			t.Cleanup(func() { _ = reopened.Close() })
			return reopened.OperationalServices()
		}
		return reopen(), reopen
	}
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL is required for durable erasure recovery tests")
	}
	store, err := operationalpostgres.New(ctx, dsn, namespace, operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.DeleteNamespace(cleanup); err != nil {
			t.Error(err)
		}
		_ = store.Close()
	})
	reopen := func() operational.OperationalServices {
		reopened, err := operationalpostgres.Open(ctx, dsn, namespace, operationalpostgres.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		return reopened.OperationalServices()
	}
	return store.OperationalServices(), reopen
}

func erasureQueue(services operational.OperationalServices) Queue {
	return Queue{Tasks: services.Queue, Admission: services.Admission, Metadata: services.Collection,
		Leases: services.Leases, Deliveries: services.Deliveries, Metrics: services.IngestionMetrics}
}
