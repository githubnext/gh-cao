package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestMergeBenchmarkDefinitionsDeclaresDashboardNames(t *testing.T) {
	database := []query.Definition{{Name: "runs-base", From: "$runs"}}
	dashboard := []query.Definition{{Name: "overview", From: "runs-base"}, {Name: "cso-summary", From: "runs-base"}}

	definitions, defined := mergeBenchmarkDefinitions(database, dashboard)

	if len(definitions) != len(database)+len(dashboard) {
		t.Fatalf("expected %d merged definitions, got %d", len(database)+len(dashboard), len(definitions))
	}
	if defined["runs-base"] {
		t.Fatal("database-only queries must not be in the declared candidate set")
	}
	if !defined["overview"] || !defined["cso-summary"] {
		t.Fatalf("expected dashboard queries to be declared, got %+v", defined)
	}
}

func TestValidateBenchmarkCandidatesRejectsUndeclared(t *testing.T) {
	defined := map[string]bool{"overview": true}

	if err := validateBenchmarkCandidates([]string{"overview"}, defined); err != nil {
		t.Fatalf("expected declared candidate to pass, got %v", err)
	}
	err := validateBenchmarkCandidates([]string{"overview", "not-a-dashboard-query"}, defined)
	if err == nil || !strings.Contains(err.Error(), "not-a-dashboard-query") {
		t.Fatalf("expected an undeclared-candidate error naming the query, got %v", err)
	}
}

func TestLoadBenchmarkDefinitionsMergesAndValidatesCandidates(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "database.json")
	dashboardPath := filepath.Join(directory, "dashboard.json")
	if err := os.WriteFile(databasePath, []byte(`[{"name":"runs-base","from":"$runs","select":[{"field":"id"}]}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dashboardPath, []byte(`[{"name":"overview","from":"runs-base","select":[{"field":"id"}]}]`), 0600); err != nil {
		t.Fatal(err)
	}

	definitions, err := loadBenchmarkDefinitions(databasePath, dashboardPath, "2024-01-01T00:00:00Z", []string{"overview"})
	if err != nil {
		t.Fatalf("expected a declared candidate to load, got %v", err)
	}
	if len(definitions) != 2 {
		t.Fatalf("expected the merged database and dashboard definitions, got %d", len(definitions))
	}

	if _, err := loadBenchmarkDefinitions(databasePath, dashboardPath, "2024-01-01T00:00:00Z", []string{"runs-base"}); err == nil {
		t.Fatal("a database-only query must not be a usable candidate")
	}
	if _, err := loadBenchmarkDefinitions(filepath.Join(directory, "missing.json"), dashboardPath, "", []string{"overview"}); err == nil {
		t.Fatal("a missing database-queries file must fail closed")
	}
	if _, err := loadBenchmarkDefinitions(databasePath, filepath.Join(directory, "missing.json"), "", []string{"overview"}); err == nil {
		t.Fatal("a missing dashboard-queries file must fail closed")
	}
}

func TestBenchmarkPostgresConfigLocalOnly(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint string
		allowed        bool
	}{
		{"ipv4", "postgres://user@127.0.0.1:5432/cao?sslmode=disable", true},
		{"ipv6", "postgres://user@[::1]:5432/cao?sslmode=disable", true},
		{"unix socket", "host=/var/run/postgresql dbname=cao", true},
		{"remote TLS", "host=db.example.com user=secret sslmode=require", false},
		{"remote IP", "host=192.0.2.1 sslmode=require", false},
		{"localhost DNS", "host=localhost", false},
		{"remote fallback", "host=127.0.0.1,db.example.com sslmode=require", false},
		{"remote fallback IP", "host=127.0.0.1,192.0.2.1 sslmode=require", false},
		{"invalid", "postgres://bad host/cao", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := benchmarkPostgresConfig(tc.endpoint)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t, error=%v", tc.allowed, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("error leaked credentials")
			}
		})
	}
}

func TestBenchmarkPostgresConfigRejectsRemoteEnvironmentFallback(t *testing.T) {
	t.Setenv("PGHOST", "db.example.com")
	if _, err := benchmarkPostgresConfig("dbname=cao"); err == nil {
		t.Fatal("environment-provided remote host must be rejected")
	}
}

func TestIsLocalPostgresHost(t *testing.T) {
	for _, tc := range []struct {
		name, host string
		local      bool
	}{
		{"ipv4 loopback", "127.0.0.1", true},
		{"ipv6 loopback", "::1", true},
		{"unix socket directory", "/var/run/postgresql", true},
		{"relative path is not a socket directory", "var/run/postgresql", false},
		{"remote ip", "192.0.2.1", false},
		{"hostname", "db.example.com", false},
		{"localhost dns name is not a parsed loopback address", "localhost", false},
		{"empty host", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalPostgresHost(tc.host); got != tc.local {
				t.Fatalf("isLocalPostgresHost(%q) = %t, want %t", tc.host, got, tc.local)
			}
		})
	}
}

func TestBenchmarkNamespaceUnique(t *testing.T) {
	first, err := newBenchmarkNamespace()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newBenchmarkNamespace()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !regexp.MustCompile(`^query_cost_benchmark_[0-9a-f]{32}$`).MatchString(first) {
		t.Fatalf("expected distinct random benchmark namespaces, got %q and %q", first, second)
	}
}

func TestBenchmarkQueriesRequiresCandidates(t *testing.T) {
	_, err := benchmarkQueries(t.Context(), nil, "", "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no query candidates") {
		t.Fatalf("expected an empty candidate error, got %v", err)
	}
}

func TestBenchmarkQueriesRejectsEmptyEvidence(t *testing.T) {
	for _, counts := range []map[string]int{{}, {"$runs": 0, "$repositories": 20}} {
		if _, err := validateBenchmarkEvidence(counts); err == nil {
			t.Fatalf("empty run data must fail closed: %+v", counts)
		}
	}
	if records, err := validateBenchmarkEvidence(map[string]int{"$runs": 2, "$repositories": 3}); err != nil || records != 5 {
		t.Fatalf("expected nonempty projection, got records=%d err=%v", records, err)
	}
}

func TestClassifyBenchmarkEvidenceIdentifiesEachRejectionStage(t *testing.T) {
	cases := []struct {
		name    string
		records int
		counts  map[string]int
		want    benchmarkEvidenceRejectionStage
	}{
		{"no sources at all", 0, map[string]int{}, benchmarkEvidenceRejectionStageNoRecord},
		{"records present but zero $runs", 20, map[string]int{"$runs": 0, "$repositories": 20}, benchmarkEvidenceRejectionStageNoRuns},
		{"missing $runs key entirely", 20, map[string]int{"$repositories": 20}, benchmarkEvidenceRejectionStageNoRuns},
		{"nonempty projection with runs", 5, map[string]int{"$runs": 2, "$repositories": 3}, benchmarkEvidenceRejectionStageNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBenchmarkEvidence(tc.records, tc.counts); got != tc.want {
				t.Fatalf("classifyBenchmarkEvidence(%d, %+v) = %q, want %q", tc.records, tc.counts, got, tc.want)
			}
		})
	}
}

func TestBuildBenchmarkMeasurementRejectsUnavailableSource(t *testing.T) {
	sources := map[string]model.Source{
		"overview": {Metadata: model.Metadata{"availability": "unavailable"}},
	}
	if _, err := buildBenchmarkMeasurement("overview", time.Millisecond, sources, model.Metrics{}); err == nil ||
		!strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected an unavailable-source error, got %v", err)
	}
	if _, err := buildBenchmarkMeasurement("missing", time.Millisecond, map[string]model.Source{}, model.Metrics{}); err == nil ||
		!strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected an unavailable error for a missing source, got %v", err)
	}
}

func TestBuildBenchmarkMeasurementRejectsInvalidPageCardinality(t *testing.T) {
	sources := map[string]model.Source{
		"overview": {
			Rows:     []model.Row{{"id": "a"}, {"id": "b"}},
			Metadata: model.Metadata{"total-row-count": 1},
		},
	}
	if _, err := buildBenchmarkMeasurement("overview", time.Millisecond, sources, model.Metrics{}); err == nil ||
		!strings.Contains(err.Error(), "invalid page cardinality") {
		t.Fatalf("expected an invalid page cardinality error, got %v", err)
	}
	missingCount := map[string]model.Source{"overview": {Rows: []model.Row{{"id": "a"}}, Metadata: model.Metadata{}}}
	if _, err := buildBenchmarkMeasurement("overview", time.Millisecond, missingCount, model.Metrics{}); err == nil ||
		!strings.Contains(err.Error(), "invalid page cardinality") {
		t.Fatalf("expected an invalid page cardinality error for a missing total-row-count, got %v", err)
	}
}

func TestBuildBenchmarkMeasurementReturnsMeasurement(t *testing.T) {
	sources := map[string]model.Source{
		"overview": {
			Rows:     []model.Row{{"id": "a"}, {"id": "b"}},
			Metadata: model.Metadata{"total-row-count": 5},
		},
	}
	metrics := model.Metrics{QueryCount: 3}
	measurement, err := buildBenchmarkMeasurement("overview", 2500*time.Microsecond, sources, metrics)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.Query != "overview" || measurement.Rows != 2 || measurement.TotalRows != 5 ||
		measurement.DurationMS != 2.5 || measurement.Metrics.QueryCount != 3 {
		t.Fatalf("unexpected measurement: %+v", measurement)
	}
}

func TestBenchmarkQueriesWithDeployedSubset(t *testing.T) {
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL is not set")
	}
	if _, err := benchmarkPostgresConfig(url); err != nil {
		t.Skip("POSTGRES_URL is not a local test instance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	namespace, err := newBenchmarkNamespace()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	schema := "cao_benchmark_" + namespace
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	store, err := postgresx.NewConfig(ctx, config, namespace)
	if err != nil {
		t.Fatal("cannot initialize test Postgres")
	}
	defer func() { _ = store.Close() }()
	defer func() {
		if err := store.DeleteNamespace(context.Background()); err != nil {
			t.Errorf("cleanup benchmark namespace: %v", err)
		}
	}()
	databasePath := "../../../dashboard/site/src/data/queries/database.json"
	dashboardPath := "../../../dashboard/site/src/agent/queries.generated.json"
	source, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	report, err := benchmarkQueries(ctx, store, source, databasePath, dashboardPath, []string{"repository-activity"})
	if err != nil {
		t.Fatal(err)
	}

	if report.Engine != "postgres-native-sql" || report.Records == 0 || report.SourceCounts["$runs"] == 0 {
		t.Fatalf("invalid Postgres ingestion report: %+v", report)
	}

	if len(report.Measurements) != 1 || report.Measurements[0].Query != "repository-activity" ||
		report.Measurements[0].DurationMS < 0 || report.Measurements[0].Metrics.QueryCount == 0 {
		t.Fatalf("missing production Go query metrics: %+v", report.Measurements)
	}
	if report.PageLimit != benchmarkPageLimit || report.Measurements[0].Rows != min(report.Measurements[0].TotalRows, report.PageLimit) {
		t.Fatalf("benchmark must report bounded page and total rows: %+v", report)
	}
	if _, err := benchmarkQueries(ctx, store, source, databasePath, dashboardPath, []string{"not-a-dashboard-query"}); err == nil {
		t.Fatal("unknown candidate must fail closed")
	}
}

func TestBenchmarkCleanupPreservesOtherNamespaces(t *testing.T) {
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL is not set")
	}
	if _, err := benchmarkPostgresConfig(url); err != nil {
		t.Skip("POSTGRES_URL is not a local test instance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	one, err := newBenchmarkNamespace()
	if err != nil {
		t.Fatal(err)
	}
	two, err := newBenchmarkNamespace()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	schema := "cao_benchmark_" + one
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	first, err := postgresx.NewConfig(ctx, config, one)
	if err != nil {
		t.Fatal("cannot initialize test Postgres")
	}
	defer func() { _ = first.Close() }()
	defer func() { _ = first.DeleteNamespace(context.Background()) }()
	second, err := postgresx.NewConfig(ctx, config, two)
	if err != nil {
		t.Fatal("cannot initialize test Postgres")
	}
	defer func() { _ = second.Close() }()
	defer func() { _ = second.DeleteNamespace(context.Background()) }()
	for _, store := range []*postgresx.Store{first, second} {
		writer, err := store.BeginIngestion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for name, row := range map[string]model.Row{
			"$repositories": {"id": "repo"}, "$workflows": {"id": "workflow", "repositoryId": "repo"}, "$runs": {"id": "test", "workflowId": "workflow", "repositoryId": "repo"},
		} {
			if err := writer.Append(ctx, name, row); err != nil {
				writer.Abort(ctx)
				t.Fatal(err)
			}
		}
		if _, err := writer.Publish(ctx, "benchmark-test"); err != nil {
			writer.Abort(ctx)
			t.Fatal(err)
		}
	}
	if err := first.DeleteNamespace(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := first.State(ctx)
	if err != nil || state.Ready {
		t.Fatalf("deleted namespace still ready: %+v, %v", state, err)
	}
	state, err = second.State(ctx)
	if err != nil || !state.Ready || state.Counts["$runs"] != 1 {
		t.Fatalf("other namespace was damaged: %+v, %v", state, err)
	}
}

func TestValidateBenchmarkFlagsRequiresSourceAndCandidates(t *testing.T) {
	for _, tc := range []struct {
		name, source, candidatesPath string
		wantFailure                  benchmarkFlagFailure
		wantErr                      bool
	}{
		{"both set", "artifacts", "candidates.json", benchmarkFlagFailureNone, false},
		{"missing source", "", "candidates.json", benchmarkFlagFailureSource, true},
		{"missing candidates", "artifacts", "", benchmarkFlagFailureCandidates, true},
		{"missing both reports source first", "", "", benchmarkFlagFailureSource, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure, err := validateBenchmarkFlags(tc.source, tc.candidatesPath)
			if failure != tc.wantFailure {
				t.Fatalf("failure = %q, want %q", failure, tc.wantFailure)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %t", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "--source and --candidates are required") {
				t.Fatalf("unexpected error message: %v", err)
			}
		})
	}
}

func TestLoadBenchmarkCandidatesReadsValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	if err := os.WriteFile(path, []byte(`["overview","cso-summary"]`), 0600); err != nil {
		t.Fatal(err)
	}
	candidates, err := loadBenchmarkCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0] != "overview" || candidates[1] != "cso-summary" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}

func TestLoadBenchmarkCandidatesRejectsMissingFile(t *testing.T) {
	_, err := loadBenchmarkCandidates(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !strings.Contains(err.Error(), "read candidates") {
		t.Fatalf("expected a read error, got %v", err)
	}
}

func TestLoadBenchmarkCandidatesRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	if err := os.WriteFile(path, []byte(`not json`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadBenchmarkCandidates(path)
	if err == nil || !strings.Contains(err.Error(), "parse candidates") {
		t.Fatalf("expected a parse error, got %v", err)
	}
}

func TestLoadBenchmarkCandidatesRejectsEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	if err := os.WriteFile(path, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadBenchmarkCandidates(path)
	if err == nil || !strings.Contains(err.Error(), "no query candidates") {
		t.Fatalf("expected an empty-candidates error, got %v", err)
	}
}

func TestBenchmarkExecutionOptionsBoundsPageAndRaisesBudgets(t *testing.T) {
	options := benchmarkExecutionOptions("runs-overview")
	if len(options.Pages) != 1 || options.Pages["runs-overview"].Limit != benchmarkPageLimit {
		t.Fatalf("pages = %#v, want only runs-overview limited to %d", options.Pages, benchmarkPageLimit)
	}
	limits := options.ResourceLimits
	if limits == nil {
		t.Fatal("resource limits missing")
	}
	if limits.MaxInputRows != query.MaxWorkingRows ||
		limits.MaxOperations != 10*query.MaxOperations ||
		limits.MaxWorkingBytes != 4*query.MaxWorkingBytes {
		t.Fatalf("limits = %#v, want benchmark budgets", limits)
	}
}
