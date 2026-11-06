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

func TestNativeControlRecoveryUsesSeparateProcessAndDurableValueHint(t *testing.T) {
	data, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, dsn, namespace := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	scope := nativeScope(t, enrollment, "octo/api")
	if _, err := queue.enqueueNativeControl(t.Context(), Task{Repository: scope.Repository, InstallationID: scope.InstallationID,
		ScopeEpoch: scope.Epoch, ValueRefresh: true, EnqueuedAt: time.Now().UTC()}, IntentValueRefresh, "", 0); err != nil {
		t.Fatal(err)
	}
	abandoned, err := queue.nativeControlLease(t.Context(), "same-consumer", time.Hour)
	if err != nil || len(abandoned) != 1 {
		t.Fatalf("initial control lease: %+v %v", abandoned, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- invokes this test executable without external collector tooling.
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestNativeControlRecoverySubprocess$", "-test.v")
	child.Env = append(os.Environ(), "PATH=/nonexistent", "CAO_NATIVE_CONTROL_DSN="+dsn,
		"CAO_NATIVE_CONTROL_NAMESPACE="+namespace, "CAO_NATIVE_CONTROL_SCHEMA="+config.RuntimeParams["search_path"])
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("separate native control process failed: %v\n%s", err, output)
	}
	if err := queue.completeNativeControl(t.Context(), abandoned[0]); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("superseded process acknowledged successor's control receipt: %v", err)
	}
	hint, err := data.Acquisition().ValueHint(t.Context(), scope)
	if err != nil || hint.Dirty != 1 || hint.Processed != 0 {
		t.Fatalf("successor lost or accepted the durable invalidation: %+v %v", hint, err)
	}
	status, err := queue.nativeStatus(t.Context())
	if err != nil || status.pending != 0 || status.depth != 0 {
		t.Fatalf("successful successor left abandoned work: %+v %v", status, err)
	}
}

func TestNativeControlRecoverySubprocess(t *testing.T) {
	dsn := os.Getenv("CAO_NATIVE_CONTROL_DSN")
	if dsn == "" {
		t.Skip("invoked by separate-process native control recovery")
	}
	if os.Getenv("PATH") != "/nonexistent" {
		t.Fatal("native control has executable prerequisites")
	}
	store, err := operationalpostgres.Open(t.Context(), dsn, os.Getenv("CAO_NATIVE_CONTROL_NAMESPACE"), operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = os.Getenv("CAO_NATIVE_CONTROL_SCHEMA")
	data, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	queue := Queue{Tasks: store}
	leases, err := queue.nativeControlLease(t.Context(), "same-consumer", 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("durable control not recoverable: %+v %v", leases, err)
	}
	worker := NativeWorker{Enrollment: Enrollment{Authority: data.NativeEnrollment()}, Values: data.Acquisition()}
	if err := worker.process(t.Context(), leases[0].Task); err != nil {
		t.Fatal(err)
	}
	if err := queue.completeNativeControl(t.Context(), leases[0]); err != nil {
		t.Fatal(err)
	}
}
