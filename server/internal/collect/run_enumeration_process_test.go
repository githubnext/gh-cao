package collect

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/operational"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeEnumerationResumesAbandonedPageInSeparateProcess(t *testing.T) {
	data, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, dsn, namespace := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	backfill := Backfill{Queue: queue, WindowDays: 7, AcquisitionScopes: enrollment.Authority, runBefore: before}
	if _, _, err := backfill.enqueueHistoricalRuns(t.Context(), []enrolledRepository{{name: "octo/api", installationID: 7}}); err != nil {
		t.Fatal(err)
	}
	abandoned, err := queue.LeaseRuns(t.Context(), "terminated", 1)
	if err != nil || len(abandoned) != 1 {
		t.Fatalf("initial enumeration lease: %v %v", abandoned, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- invokes this test executable, never external collection tooling.
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestNativeEnumerationRecoverySubprocess$", "-test.v")
	child.Env = append(os.Environ(), "PATH=/nonexistent",
		"CAO_NATIVE_DISCOVERY_DSN="+dsn, "CAO_NATIVE_DISCOVERY_NAMESPACE="+namespace,
		"CAO_NATIVE_DISCOVERY_SCHEMA="+config.RuntimeParams["search_path"],
		"CAO_NATIVE_DISCOVERY_BEFORE="+before.Format(time.RFC3339))
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("separate native enumeration process failed: %v\n%s", err, output)
	}
	if err := queue.CompleteRun(t.Context(), abandoned[0]); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("terminated instance could acknowledge successor's page: %v", err)
	}
	resumed, err := queue.LeaseRuns(t.Context(), "inspect", 1)
	if err != nil || len(resumed) != 1 || resumed[0].Task.Enumeration == nil || resumed[0].Task.Enumeration.WorkflowID != 41 ||
		!resumed[0].Task.Enumeration.Before.Equal(before) || resumed[0].Task.Failures != 0 {
		t.Fatalf("separate process lost the frozen cursor: %v %v", resumed, err)
	}
}

func TestNativeEnumerationRecoverySubprocess(t *testing.T) {
	dsn := os.Getenv("CAO_NATIVE_DISCOVERY_DSN")
	if dsn == "" {
		t.Skip("invoked by separate-process native enumeration recovery")
	}
	if os.Getenv("PATH") != "/nonexistent" {
		t.Fatal("native enumeration recovery has executable prerequisites")
	}
	store, err := operationalpostgres.Open(t.Context(), dsn, os.Getenv("CAO_NATIVE_DISCOVERY_NAMESPACE"), operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = os.Getenv("CAO_NATIVE_DISCOVERY_SCHEMA")
	data, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	before, err := time.Parse(time.RFC3339, os.Getenv("CAO_NATIVE_DISCOVERY_BEFORE"))
	if err != nil {
		t.Fatal(err)
	}
	queue := Queue{Tasks: store}
	leases, err := queue.ReclaimRuns(t.Context(), "successor-process", 0, 1)
	if err != nil || len(leases) != 1 || leases[0].Task.Enumeration == nil {
		t.Fatalf("durable page not recoverable: %v %v", leases, err)
	}
	fixture := &nativeHistoryFixture{after: before.Add(-7 * 24 * time.Hour), before: before}
	worker := RunWorker{Queue: queue, Discovery: fixture, Scopes: data.Acquisition()}
	if err := worker.process(t.Context(), leases[0]); err != nil {
		t.Fatal(err)
	}
}
