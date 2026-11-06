package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

type focusedOperationalStore struct {
	operational.Backend
	services operational.OperationalServices
}

func (s focusedOperationalStore) OperationalServices() operational.OperationalServices {
	return s.services
}

func focusedTestStore(store operational.Store) focusedOperationalStore {
	s := store.OperationalServices()
	return focusedOperationalStore{
		Backend: store,
		services: operational.OperationalServices{
			Backend:            struct{ operational.Backend }{s.Backend},
			Cache:              struct{ operational.Cache }{s.Cache},
			RequestLimiter:     struct{ operational.RequestLimiter }{s.RequestLimiter},
			Sessions:           struct{ operational.SessionStore }{s.Sessions},
			SessionInvalidator: struct{ operational.SessionInvalidator }{s.SessionInvalidator},
			Revocations:        struct{ operational.RevocationQueue }{s.Revocations},
			Leases:             struct{ operational.LeaseStore }{s.Leases},
			State:              struct{ operational.StateStore }{s.State},
			Deliveries: struct {
				operational.DeliveryDeduplicator
			}{s.Deliveries},
			Queue: struct{ operational.TaskQueue }{s.Queue},
			Admission: struct {
				operational.DeliveryAdmissionStore
			}{s.Admission},
			Collection:  struct{ operational.CollectionMetadata }{s.Collection},
			GitHubQuota: struct{ operational.GitHubQuotaStore }{s.GitHubQuota},
			RateLimits: struct {
				operational.RateLimitStateStore
			}{s.RateLimits},
			Health:           struct{ operational.HealthProbe }{s.Health},
			IngestionMetrics: struct{ operational.IngestionMetrics }{s.IngestionMetrics},
			CLICommands:      s.CLICommands,
			Audit:            s.Audit,
		},
	}
}

func TestFocusedServicesComposeApplicationAndValidateCollection(t *testing.T) {
	store := focusedTestStore(redisx.NewStore(emptyRedisClient{}, "focused-services"))
	app, err := New(t.Context(), store, Config{
		Database: constructorDatabase(), SiteDirectory: t.TempDir(),
		Listen: "127.0.0.1:8080", AccessToken: strings.Repeat("x", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.services != store.services {
		t.Fatal("application substituted the selected focused services")
	}
	if err := operational.ValidateOperationalServices(store.Capabilities(), store.services,
		operational.Requirements{SingleProcess: true, AllowVolatile: true, Collection: true}); err != nil {
		t.Fatalf("focused collection services were rejected: %v", err)
	}
}

func TestCollectorRejectsMissingFocusedServices(t *testing.T) {
	var absent *redisx.Store
	for name, remove := range map[string]func(*operational.OperationalServices){
		"queue":       func(s *operational.OperationalServices) { s.Queue = absent },
		"admission":   func(s *operational.OperationalServices) { s.Admission = nil },
		"metadata":    func(s *operational.OperationalServices) { s.Collection = absent },
		"leases":      func(s *operational.OperationalServices) { s.Leases = nil },
		"quota":       func(s *operational.OperationalServices) { s.GitHubQuota = absent },
		"rate limits": func(s *operational.OperationalServices) { s.RateLimits = nil },
		"metrics":     func(s *operational.OperationalServices) { s.IngestionMetrics = absent },
	} {
		t.Run(name, func(t *testing.T) {
			store := focusedTestStore(redisx.NewStore(emptyRedisClient{}, "missing-focused-services"))
			remove(&store.services)
			err := operational.ValidateOperationalServices(store.Capabilities(), store.services,
				operational.Requirements{SingleProcess: true, AllowVolatile: true, Collection: true})
			if !errors.Is(err, operational.ErrUnsupported) {
				t.Fatalf("incomplete collector services accepted: %v", err)
			}
		})
	}
}
