package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/query"
)

type usageQuotaStore struct {
	operational.GitHubQuotaStore
	samples []operational.GitHubQuotaUsageSample
	now     time.Time
	err     error
}

func (store usageQuotaStore) GitHubQuotaUsage(context.Context) ([]operational.GitHubQuotaUsageSample, time.Time, error) {
	return store.samples, store.now, store.err
}

func newUsageQuota(t *testing.T, store usageQuotaStore) *githubquota.Service {
	t.Helper()
	service, err := githubquota.New(store, githubquota.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

var usageNow = time.Date(2026, 9, 30, 15, 5, 0, 0, time.UTC)

func usageSamples() []operational.GitHubQuotaUsageSample {
	slot := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)
	return []operational.GitHubQuotaUsageSample{
		{Bucket: "collector:123:core", Slot: slot, Limit: 5000, Used: 1000, Reserved: 20},
		{Bucket: "backfill:123:core", Slot: slot, Limit: 15000, Used: 9000},
	}
}

func TestGitHubQuotaUsageRequiresHostedAdministrator(t *testing.T) {
	app := &App{
		oauth:  &githubOAuth{},
		config: Config{AdminUsers: []string{"operator"}},
		quota:  newUsageQuota(t, usageQuotaStore{samples: usageSamples(), now: usageNow}),
	}
	request := httptest.NewRequestWithContext(
		context.WithValue(t.Context(), oauthSessionContextKey{}, oauthSession{Login: "reader"}),
		http.MethodGet, "/api/v1/github-quota/usage", nil,
	)
	response := httptest.NewRecorder()
	app.gitHubQuotaUsage(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-admin usage request returned %d", response.Code)
	}

	request = httptest.NewRequestWithContext(
		context.WithValue(t.Context(), oauthSessionContextKey{}, assuredSession("operator")),
		http.MethodGet, "/api/v1/github-quota/usage", nil,
	)
	response = httptest.NewRecorder()
	app.gitHubQuotaUsage(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("administrator usage request returned %d: %s", response.Code, response.Body.String())
	}
	var report githubquota.UsageReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.IntervalSeconds != 900 || !report.GeneratedAt.Equal(usageNow) || len(report.Buckets) != 2 ||
		report.Buckets[0].Bucket.App != "backfill" || report.Buckets[0].UsagePercent != 60 ||
		len(report.Aggregate) != 1 || report.Aggregate[0].Used != 10000 || report.Aggregate[0].UsagePercent != 50 {
		t.Fatalf("unexpected usage report %+v", report)
	}
}

func TestGitHubQuotaUsageReportsUnavailableStore(t *testing.T) {
	for name, app := range map[string]*App{
		"unconfigured": {},
		"failing":      {quota: newUsageQuota(t, usageQuotaStore{err: errors.New("operational down")})},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.gitHubQuotaUsage(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/github-quota/usage", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("usage request returned %d", response.Code)
			}
		})
	}
}

func TestGitHubQuotaUsageQueryRunsThroughServerQueryEngine(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	app := &App{services: newTestOperationalStore(t).OperationalServices(), database: database,
		quota: newUsageQuota(t, usageQuotaStore{samples: usageSamples(), now: usageNow}),
	}
	input := queryRequest{
		Queries:     []query.Definition{{Name: "github-api-usage", From: gitHubQuotaUsageSourceName}},
		SourceNames: []string{"github-api-usage"},
	}
	for _, test := range []struct {
		name       string
		authorized bool
		want       string
		rows       int
	}{
		{name: "authorized", authorized: true, want: "available", rows: 3},
		{name: "unauthorized", authorized: false, want: "unavailable", rows: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, status, err := app.executeQuery(t.Context(), input, test.authorized)
			if err != nil || status != http.StatusOK {
				t.Fatalf("executeQuery() status=%d err=%v", status, err)
			}
			source := result.Sources["github-api-usage"]
			if source.Metadata["availability"] != test.want || len(source.Rows) != test.rows {
				t.Fatalf("query returned unexpected usage source: %+v", source)
			}
			if !test.authorized {
				return
			}
			aggregate := source.Rows[0]
			if aggregate["scope"] != "aggregate" || aggregate["bucket"] != gitHubQuotaAggregateBucket ||
				aggregate["observed-at"] != "2026-09-30T15:00:00Z" || aggregate["usage-percent"] != json.Number("50") {
				t.Fatalf("unexpected aggregate row %+v", aggregate)
			}
			bucket := source.Rows[2]
			if bucket["scope"] != "bucket" || bucket["bucket"] != "collector/123/core" ||
				bucket["installation"] != json.Number("123") || bucket["used"] != json.Number("1000") || bucket["usage-percent"] != json.Number("20") {
				t.Fatalf("unexpected bucket row %+v", bucket)
			}
		})
	}
}
