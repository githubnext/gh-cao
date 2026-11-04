package postgresx_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func acquisitionIdentity() collectnative.Identity {
	return collectnative.Identity{Repository: "octo/api", InstallationID: 7, RunID: 303, Attempt: 1}
}

func acquisitionEvidence(identity collectnative.Identity) collectnative.Evidence {
	return collectnative.Evidence{Version: 1, Identity: identity, Run: map[string]any{
		"organization": "octo", "repository": identity.Repository, "run_id": identity.RunID, "run_attempt": identity.Attempt,
		"workflow_name": "Test",
		"workflow_path": ".github/workflows/test.lock.yml",
	}, Gaps: []string{"session-unavailable"}}
}

func TestNativeAcquisitionFencedConcurrentHandoff(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	a, b := first.Acquisition(), second.Acquisition()
	identity := acquisitionIdentity()
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan collectnative.Claim, 2)
	failures := make(chan error, 2)
	var workers sync.WaitGroup
	for _, store := range []postgresx.Acquisition{a, b} {
		workers.Go(func() {
			<-start
			claim, err := store.Claim(t.Context(), identity, 100*time.Millisecond)
			if err != nil {
				failures <- err
			} else {
				results <- claim
			}
		})
	}
	close(start)
	workers.Wait()
	if len(results) != 1 || len(failures) != 1 || !errors.Is(<-failures, collectnative.ErrBusy) {
		t.Fatal("separate database clients did not elect exactly one attempt owner")
	}
	stale := <-results
	wait := time.NewTimer(150 * time.Millisecond)
	select {
	case <-t.Context().Done():
		t.Fatal("canceled while waiting for database lease expiry")
	case <-wait.C:
	}
	current, err := b.Claim(t.Context(), identity, time.Minute)
	if err != nil || current.Generation <= stale.Generation {
		t.Fatalf("crashed claim was not reclaimable: %v %v", current, err)
	}
	if err := a.Publish(t.Context(), stale, acquisitionEvidence(identity)); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("expired worker published: %v", err)
	}
	if err := a.Release(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(t.Context(), current, acquisitionEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	retained, digest, err := a.Pending(t.Context(), identity)
	if err != nil || retained.Identity != identity || digest == "" {
		t.Fatalf("separate process handoff unavailable: %v %v", retained, err)
	}
	retry, err := a.Claim(t.Context(), identity, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), retry, acquisitionEvidence(identity)); err != nil {
		t.Fatal(err)
	}
	_, retriedDigest, err := b.Pending(t.Context(), identity)
	if err != nil || retriedDigest != digest {
		t.Fatalf("retry changed immutable evidence identity: %s %s %v", digest, retriedDigest, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- os.Executable identifies this test binary, not caller input.
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestNativeAcquisitionSubprocess$", "-test.v")
	command.Env = []string{"PATH=" + t.TempDir(), "CAO_NATIVE_HANDOFF_DSN=" + config.ConnString(),
		"CAO_NATIVE_HANDOFF_SCHEMA=" + config.RuntimeParams["search_path"]}
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "durable exact-attempt handoff verified") {
		t.Fatalf("independent process replay with Node unavailable: %v %s", err, output)
	}
}

func TestNativeAcquisitionSubprocess(t *testing.T) {
	dsn := os.Getenv("CAO_NATIVE_HANDOFF_DSN")
	if dsn == "" {
		t.Skip("invoked by the parent cross-process handoff test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = os.Getenv("CAO_NATIVE_HANDOFF_SCHEMA")
	store, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	evidence, _, err := store.Acquisition().Pending(t.Context(), acquisitionIdentity())
	if err != nil || evidence.Identity != acquisitionIdentity() {
		t.Fatalf("replayed evidence identity: %v %v", evidence, err)
	}
	if count, err := store.Acquisition().Replay(t.Context(), 1); err != nil || count != 1 {
		t.Fatalf("independent process could not activate durable evidence: %d %v", count, err)
	}
	results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{{Name: "native-run", From: "$runs"}}, []string{"native-run"})
	if err != nil || len(results["native-run"].Rows) != 1 {
		t.Fatalf("independent process canonical consumer failed: %v", err)
	}
	t.Log("durable exact-attempt handoff verified")
}

func TestNativeAcquisitionWithdrawalAndAttemptOwnership(t *testing.T) {
	store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	a := store.Acquisition()
	identity := acquisitionIdentity()
	if _, err := a.Claim(t.Context(), identity, time.Minute); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("invented enrollment accepted: %v", err)
	}
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	claim, err := a.Claim(t.Context(), identity, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	next := identity
	next.Attempt++
	evidence := acquisitionEvidence(next)
	if err := a.Publish(t.Context(), claim, evidence); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("old attempt claim published a rerun: %v", err)
	}
	if err := a.SetEnrollment(t.Context(), identity.Repository, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), claim, acquisitionEvidence(identity)); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("withdrawn/restored owner reused an old claim: %v", err)
	}
	newClaim, err := a.Claim(t.Context(), next, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), newClaim, evidence); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Pending(t.Context(), identity); err == nil {
		t.Fatal("old attempt resolved to the newer attempt's evidence")
	}
	if err := a.SetEnrollment(t.Context(), identity.Repository, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Pending(t.Context(), next); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("withdrawn evidence remained readable: %v", err)
	}
}

func TestNativeAcquisitionCancellationAndExpiredPublication(t *testing.T) {
	store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	a := store.Acquisition()
	identity := acquisitionIdentity()
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	claim, err := a.Claim(t.Context(), identity, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := a.Publish(t.Context(), claim, acquisitionEvidence(identity)); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("expired but unreclaimed worker published: %v", err)
	}
	claim, err = a.Claim(t.Context(), identity, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.Publish(ctx, claim, acquisitionEvidence(identity)); err == nil {
		t.Fatal("canceled publication succeeded")
	}
	if _, _, err := a.Pending(t.Context(), identity); err == nil {
		t.Fatal("failed publication stranded partial evidence")
	}
}
