package postgresx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeAcquisitionRetentionPreservesUnconsumedIntent(t *testing.T) {
	store, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	acquisition := store.Acquisition()
	identity := acquisitionIdentity()
	if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	publication := publishNative(t, acquisition, acquisitionEvidence(identity))
	connection, err := pgx.ConnectConfig(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	if _, err := connection.Exec(t.Context(), `UPDATE cao_acquisition_attempts
		SET published_at=clock_timestamp()-interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	next := identity
	next.RunID++
	if _, err := acquisition.Claim(t.Context(), next, time.Minute); err != nil {
		t.Fatal(err)
	}
	pending, err := acquisition.Publications(t.Context(), 1)
	if err != nil || len(pending) != 1 || pending[0] != publication {
		t.Fatal("ordinary retention silently evicted durable pending publication")
	}
}

func TestNativeAcquisitionCanonicalFailureRollsBackParentsAndReceipt(t *testing.T) {
	store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	acquisition := store.Acquisition()
	identity := acquisitionIdentity()
	if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	invalid := acquisitionEvidence(identity)
	invalid.Run["created_at"] = "invalid-upstream-clock"
	publication := publishNative(t, acquisition, invalid)
	if _, err := acquisition.Activate(t.Context(), publication); err == nil {
		t.Fatal("invalid source clock published canonical data")
	}
	state, err := store.State(t.Context())
	if err != nil || state.Revision != 0 {
		t.Fatal("failed canonical publication advanced its revision")
	}
	pending, err := acquisition.Publications(t.Context(), 1)
	if err != nil || len(pending) != 1 || pending[0] != publication {
		t.Fatal("failed canonical publication consumed its recovery intent")
	}
	corrected := publishNative(t, acquisition, acquisitionEvidence(identity))
	if _, err := acquisition.Activate(t.Context(), publication); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatal("stale failed publication acknowledged corrected evidence")
	}
	if changed, err := acquisition.Activate(t.Context(), corrected); err != nil || !changed {
		t.Fatalf("corrected evidence could not recover: %t %v", changed, err)
	}
	state, err = store.State(t.Context())
	if err != nil || !state.Ready || state.Counts["$repositories"] != 1 || state.Counts["$workflows"] != 1 || state.Counts["$runs"] != 1 {
		t.Fatal("transaction rollback/retry left orphaned or duplicate canonical parents")
	}
}
