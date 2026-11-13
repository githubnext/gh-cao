package server

import (
	"context"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

// testOperationalStore models deployment guarantees for application unit tests.
// Real memory topology and restart-loss rules use the unwrapped memory adapter.
type testOperationalStore struct {
	*memory.Store
	overrides []any
}

func (s *testOperationalStore) Capabilities() operational.Capabilities {
	c := s.Store.Capabilities()
	durable := operational.Capability{Scope: operational.ScopeDeployment, Persistence: operational.PersistenceRestart}
	c.Cache, c.RequestLimits, c.Sessions, c.Revocations = durable, durable, durable, durable
	c.Collection, c.Coordination, c.GitHubQuota, c.Diagnostics = durable, durable, durable, durable
	return c
}
func newTestOperationalStore(t *testing.T, overrides ...any) *testOperationalStore {
	t.Helper()
	s, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &testOperationalStore{Store: s, overrides: overrides}
}

func (s *testOperationalStore) OperationalServices() operational.OperationalServices {
	result := s.Store.OperationalServices()
	for _, override := range s.overrides {
		if cache, ok := override.(operational.Cache); ok {
			result.Cache = cache
		}
		if limiter, ok := override.(operational.RequestLimiter); ok {
			result.RequestLimiter = limiter
		}
		if metrics, ok := override.(operational.IngestionMetrics); ok {
			result.IngestionMetrics = metrics
		}
		if queue, ok := override.(operational.TaskQueue); ok {
			result.Queue = queue
		}
	}
	result.Queue, result.IngestionMetrics = s, s
	return result
}
func (s *testOperationalStore) QueueStats(ctx context.Context, queue, group string) (operational.QueueStats, error) {
	for _, override := range s.overrides {
		if q, ok := override.(operational.TaskQueue); ok {
			return q.QueueStats(ctx, queue, group)
		}
	}
	return s.Store.QueueStats(ctx, queue, group)
}
func (s *testOperationalStore) DelayedDepth(ctx context.Context, queue string) (int64, error) {
	for _, override := range s.overrides {
		if q, ok := override.(operational.TaskQueue); ok {
			return q.DelayedDepth(ctx, queue)
		}
	}
	return s.Store.DelayedDepth(ctx, queue)
}
func (s *testOperationalStore) IngestionHealth(ctx context.Context) (map[string]int64, map[string]string, error) {
	for _, override := range s.overrides {
		if m, ok := override.(operational.IngestionMetrics); ok {
			return m.IngestionHealth(ctx)
		}
	}
	return s.Store.IngestionHealth(ctx)
}
