package main

import (
	"context"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func TestObserveSnapshotRequiredResourcesAndPartialFailure(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		resources []string
		graphql   githubquota.Observation
		present   bool
		wantError bool
	}{
		{name: "only required resource", resources: []string{"core"}, present: true, graphql: githubquota.Observation{Limit: 5000, Remaining: 700, ResetAt: time.Now().Add(time.Hour)}},
		{name: "missing later resource", resources: []string{"core", "graphql"}, wantError: true},
		{name: "expired later resource", resources: []string{"core", "graphql"}, present: true, graphql: githubquota.Observation{Limit: 5000, ResetAt: time.Unix(1, 0)}, wantError: true},
		{name: "zero limit", resources: []string{"core", "graphql"}, present: true, graphql: githubquota.Observation{ResetAt: time.Now().Add(time.Hour)}, wantError: true},
		{name: "service rejects negative remaining", resources: []string{"core", "graphql"}, present: true, graphql: githubquota.Observation{Limit: 5000, Remaining: -1, ResetAt: time.Now().Add(time.Hour)}, wantError: true},
		{name: "both resources", resources: []string{"core", "graphql"}, present: true, graphql: githubquota.Observation{Limit: 5000, Remaining: 700, ResetAt: time.Now().Add(time.Hour)}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, err := memory.New(memory.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			service, err := githubquota.New(store, githubquota.Options{})
			if err != nil {
				t.Fatal(err)
			}
			b := &bridge{service: service, now: time.Now}
			current := snapshot{identity: "test-snapshot", observations: map[string]githubquota.Observation{
				"core": {Limit: 5000, Remaining: 900, ResetAt: time.Now().Add(time.Hour)},
			}}
			if scenario.present {
				current.observations["graphql"] = scenario.graphql
			}
			ctx := context.Background()
			err = b.observeSnapshot(ctx, current, scenario.resources)
			if (err != nil) != scenario.wantError {
				t.Fatalf("observeSnapshot error = %v, want error %v", err, scenario.wantError)
			}
			core, err := service.State(ctx, bucket(current.identity, "core"))
			if err != nil || core.Remaining != 900 {
				t.Fatalf("earlier observation not retained: %+v, %v", core, err)
			}
			graphql, err := service.State(ctx, bucket(current.identity, "graphql"))
			if err != nil {
				t.Fatal(err)
			}
			if len(scenario.resources) == 2 && !scenario.wantError {
				if graphql.Remaining != 700 {
					t.Fatalf("required observation not applied: %+v", graphql)
				}
			} else if graphql.Status != githubquota.StatusUnknown {
				t.Fatalf("unusable or unrequested resource was observed: %+v", graphql)
			}
		})
	}
}
