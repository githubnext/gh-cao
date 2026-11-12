package collect

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

// publishedSnapshot is a real snapshot published by the Activity workflow.
const publishedSnapshot = "../../testdata/deployed-subset"

func TestBackfillRejectsLegacySnapshotReplay(t *testing.T) {
	b := volatileBackfill(t)
	snapshot, _ := testutil.RecentSnapshot(t, publishedSnapshot)
	copyTree(t, snapshot, b.Lake.Directory)
	t.Setenv("PATH", t.TempDir())
	if _, err := b.Replay(t.Context()); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("snapshot lake was accepted as native recovery: %v", err)
	}
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
