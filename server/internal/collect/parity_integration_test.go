package collect

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

// publishedSnapshot is a real snapshot published by the Activity workflow. The
// collection profile writes evidence in exactly this layout, which is what
// makes the two profiles interchangeable.
const publishedSnapshot = "../../testdata/deployed-subset"

// databaseQueries is the canonical projection query document both profiles
// project through.
const databaseQueries = "../../../dashboard/site/src/data/queries/database.json"

// TestProfilesProduceIdenticalCanonicalRecords is the equivalence test between
// the two ingestion profiles.
//
// It ingests the published snapshot directly, which is what the Actions
// profile does, then ingests the same bytes through the collection profile's
// evidence lake and projector. Both must produce the same canonical
// collections and counts, because both run the same projector over the same
// layout.
func TestProfilesProduceIdenticalCanonicalRecords(t *testing.T) {
	_, ctx := integrationStore(t)
	actionsData := integrationPostgres(t, ctx)
	snapshot, _ := testutil.RecentSnapshot(t, publishedSnapshot)
	actions, err := ingest.Run(ctx, actionsData, snapshot, ingest.Options{
		DatabaseQueriesPath: databaseQueries, Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	collectionStore, collectionCtx := integrationStore(t)
	collectionData := integrationPostgres(t, collectionCtx)
	if state, err := collectionData.State(collectionCtx); err != nil || state.Ready {
		t.Fatalf("collection fixture shared the Actions projection before ingestion: %+v, %v", state, err)
	}
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	copyTree(t, snapshot, lake.Directory)
	populated, err := lake.Populated()
	if err != nil {
		t.Fatal(err)
	}
	if !populated {
		t.Fatal("an evidence lake holding collected shards must be projectable")
	}
	projector := Projector{State: collectionStore, Leases: collectionStore, Data: collectionData, Lake: lake,
		Enrollment:          Enrollment{Metadata: collectionStore, Leases: collectionStore},
		DatabaseQueriesPath: databaseQueries,
	}
	collected, err := projector.Project(collectionCtx)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(actions.Counts, collected.Counts) {
		t.Fatalf("canonical counts differ between profiles:\nactions=%v\ncollected=%v",
			actions.Counts, collected.Counts)
	}
	if len(collected.Counts) == 0 {
		t.Fatal("expected the equivalence test to compare a non-empty projection")
	}
	for name := range actions.Counts {
		definitions := []query.Definition{{Name: "parity", From: name}}
		published, _, err := actionsData.ExecuteSQLPlan(ctx, definitions, []string{"parity"})
		if err != nil {
			t.Fatal(err)
		}
		replayed, _, err := collectionData.ExecuteSQLPlan(collectionCtx, definitions, []string{"parity"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(published, replayed) {
			t.Fatalf("canonical rows or quality metadata differ for %s", name)
		}
	}
}

// TestProjectionIsCoalesced proves that a burst of collections results in one
// projection rather than one projection per repository, which is what keeps
// projection cost independent of event rate.
func TestProjectionIsCoalesced(t *testing.T) {
	store, ctx := integrationStore(t)
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	projector := Projector{State: store, Leases: store, Lake: lake, Enrollment: Enrollment{Metadata: store, Leases: store},
		MinInterval: time.Hour,
	}
	pending, err := projector.PendingProjection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("a quiet collector must not request projection")
	}
	for index := 0; index < 25; index++ {
		if err := projector.RequestProjection(ctx); err != nil {
			t.Fatal(err)
		}
	}
	pending, err = projector.PendingProjection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("collections must leave exactly one pending projection request")
	}
}

func integrationPostgres(t *testing.T, ctx context.Context) *postgresx.Store {
	t.Helper()
	return testutil.Postgres(t, ctx, "CAO_TEST_POSTGRES_URL", "CAO_POSTGRES_URL", "POSTGRES_URL")
}

// TestBackfillFailsClosedWithoutEnrollment proves cold start does not invent
// scope when the App cannot be enumerated.
func TestBackfillFailsClosedWithoutEnrollment(t *testing.T) {
	store, ctx := integrationStore(t)
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	backfill := Backfill{StateStore: store, Metadata: store, Enrollment: Enrollment{Metadata: store, Leases: store}, Queue: Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store},
		Lake: lake,
	}
	if _, err := backfill.Run(ctx); err == nil {
		t.Fatal("expected cold start without enumeration to fail closed")
	}
	state, err := backfill.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "failed" {
		t.Fatalf("phase = %q, want a reported failure", state.Phase)
	}
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		reader, err := os.Open(path) // #nosec G304,G122 -- path comes from walking the test's own fixture directory.
		if err != nil {
			return err
		}
		defer func() { _ = reader.Close() }()
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		writer, err := os.Create(target) // #nosec G304 -- target is inside the test's temporary destination directory.
		if err != nil {
			return err
		}
		defer func() { _ = writer.Close() }()
		_, err = io.Copy(writer, reader)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
