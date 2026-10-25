package adxingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

type snapshotReader struct {
	session sessionexport.Session
	err     error
}

func (r *snapshotReader) ReadSession(_ context.Context, request sessionexport.Request) (sessionexport.Session, error) {
	if request.After >= "run" {
		return sessionexport.Session{Revision: r.session.Revision}, nil
	}
	return r.session, r.err
}

func testWorker(t *testing.T, reader sessionexport.Reader, client *Client) (*Worker, operational.OperationalServices) {
	t.Helper()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	services := store.OperationalServices()
	worker, err := NewWorker(testConfig(), reader, client, services)
	if err != nil {
		t.Fatal(err)
	}
	return worker, services
}

func readState(t *testing.T, worker *Worker) deliveryState {
	t.Helper()
	content, err := worker.services.State.OperationalState(t.Context(), worker.key)
	if err != nil {
		t.Fatal(err)
	}
	var state deliveryState
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func step(t *testing.T, worker *Worker) bool {
	t.Helper()
	more, err := worker.Step(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return more
}

func TestSimulatorRetryRestartAndCorrections(t *testing.T) {
	sim, client := newSimulator(t)
	reader := &snapshotReader{session: testSession()}
	worker, services := testWorker(t, reader, client)
	sim.failTable = "SessionFacts"
	if _, err := worker.Step(t.Context()); err == nil || readState(t, worker).Pending == nil {
		t.Fatal("ambiguous ingestion lost pending evidence")
	}
	if sim.latestRevision() != 0 {
		t.Fatal("published a manifest before facts were verified")
	}
	// Canonical replacement must not replace the already durable job.
	reader.session.Run["inputTokens"] = 7
	reader.session.Revision = 2
	restarted, err := NewWorker(testConfig(), reader, client, services)
	if err != nil {
		t.Fatal(err)
	}
	step(t, restarted)
	if sim.latestRevision() != 1 || readState(t, restarted).Pending != nil {
		t.Fatal("restart did not finish the immutable source version")
	}
	if step(t, restarted) {
		t.Fatal("sweep did not end")
	}
	step(t, restarted)
	if sim.latestRevision() != 2 || sim.manifestEarly {
		t.Fatal("correction did not publish after its complete facts")
	}
	if len(sim.tables["SessionFacts"]) != 3 {
		t.Fatal("simulator did not preserve retry duplicates")
	}
	if sim.tables["SessionFacts"][0]["ProjectionId"] != sim.tables["SessionFacts"][1]["ProjectionId"] {
		t.Fatal("retry changed the logical identity")
	}
}

func TestSimulatorUnchangedContentDoesNotReingestFacts(t *testing.T) {
	sim, client := newSimulator(t)
	reader := &snapshotReader{session: testSession()}
	worker, _ := testWorker(t, reader, client)
	step(t, worker)
	step(t, worker)
	before := sim.requests
	step(t, worker)
	if sim.requests != before {
		t.Fatal("unchanged canonical revision contacted ADX")
	}
	reader.session.Revision = 2
	step(t, worker)
	if len(sim.tables["SessionFacts"]) != 1 || sim.latestRevision() != 2 {
		t.Fatal("unchanged facts were reingested or ready revision did not advance")
	}
}

func TestSimulatorVisibilityStorageAndPartialQueryFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*adxSimulator)
	}{
		{"visibility", func(s *adxSimulator) { s.hideTable = "ToolEvents" }},
		{"capacity", func(s *adxSimulator) { s.bytes = 50_000_000_000 }},
		{"partial-query", func(s *adxSimulator) { s.partial = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			sim, client := newSimulator(t)
			worker, _ := testWorker(t, &snapshotReader{session: testSession()}, client)
			test.set(sim)
			if _, err := worker.Step(t.Context()); err == nil || sim.latestRevision() != 0 {
				t.Fatal("destination failure became ready success")
			}
			if readState(t, worker).Pending == nil {
				t.Fatal("destination failure lost delivery state")
			}
		})
	}
}

func TestSimulatorRecoversExpiredDestinationFacts(t *testing.T) {
	sim, client := newSimulator(t)
	reader := &snapshotReader{session: testSession()}
	worker, _ := testWorker(t, reader, client)
	step(t, worker)
	step(t, worker)
	sim.tables["ToolEvents"] = nil
	reader.session.Revision = 2
	if _, err := worker.Step(t.Context()); !errors.Is(err, ErrVisibility) {
		t.Fatalf("missing destination detail not detected: %v", err)
	}
	step(t, worker)
	if sim.latestRevision() != 2 || len(sim.tables["ToolEvents"]) != 1 {
		t.Fatal("expired detail was not repaired before the new manifest")
	}
}

func TestWorkerRecordsOversizeGapAndHonorsScope(t *testing.T) {
	_, client := newSimulator(t)
	reader := &snapshotReader{session: testSession(), err: sessionexport.ErrTooLarge}
	worker, _ := testWorker(t, reader, client)
	step(t, worker)
	state := readState(t, worker)
	if state.GapCount != 1 || state.LastGapRunID != "run" || state.Pending != nil || state.ScanRevision != 1 {
		t.Fatalf("lost explicit budget gap: %+v", state)
	}
	step(t, worker)
	if step(t, worker) {
		t.Fatal("gap caused an endless sweep")
	}
	reader.err = nil
	reader.session.Revision = 2
	reader.session.Run["repositoryId"] = "not-authorized"
	if _, err := worker.Step(t.Context()); err == nil {
		t.Fatal("exported outside configured scope")
	}
}

func TestWorkerDoesNotCallDestinationBeforeDurableCapture(t *testing.T) {
	sim, client := newSimulator(t)
	reader := &snapshotReader{session: testSession()}
	worker, services := testWorker(t, reader, client)
	worker.services.State = &failingState{StateStore: services.State}
	if _, err := worker.Step(t.Context()); err == nil || sim.requests != 0 {
		t.Fatal("remote work preceded durable capture")
	}
}

type failingState struct{ operational.StateStore }

func (*failingState) SetOperationalState(context.Context, string, []byte) error {
	return operational.ErrCapacity
}

func TestWorkerLeaseAndCancellation(t *testing.T) {
	sim, client := newSimulator(t)
	worker, _ := testWorker(t, &snapshotReader{session: testSession()}, client)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := worker.Step(ctx); err == nil || sim.requests != 0 {
		t.Fatal("cancelled worker performed remote work")
	}
	content, err := json.Marshal(deliveryState{Version: contractVersion, Pending: &Projection{RepositoryID: "forbidden"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.services.State.SetOperationalState(t.Context(), worker.key, content); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Step(t.Context()); err == nil || sim.requests != 0 {
		t.Fatal("out-of-scope pending job performed remote work")
	}
}

func TestProjectionMissingAttemptStillHasReadyJoin(t *testing.T) {
	session := testSession()
	delete(session.Run, "attempt")
	session.Facts["$tools"] = []model.Row{}
	projection, err := project(session)
	if err != nil {
		t.Fatal(err)
	}
	sim, client := newSimulator(t)
	worker, _ := testWorker(t, &snapshotReader{session: session}, client)
	step(t, worker)
	if projection.Manifest["RunAttempt"] != nil || sim.latestRevision() != 1 {
		t.Fatal("unknown attempt was replaced with a fabricated number")
	}
}

func TestWorkerExpiresPendingPayloadAsDurableGap(t *testing.T) {
	sim, client := newSimulator(t)
	worker, _ := testWorker(t, &snapshotReader{session: testSession()}, client)
	projection, err := project(testSession())
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(deliveryState{
		Version: contractVersion, ScanRevision: 1, Pending: &projection,
		PendingSince: time.Now().Add(-8 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.services.State.SetOperationalState(t.Context(), worker.key, content); err != nil {
		t.Fatal(err)
	}
	if !step(t, worker) {
		t.Fatal("expired delivery stopped the sweep")
	}
	state := readState(t, worker)
	if sim.requests != 0 || state.Pending != nil || !state.PendingSince.IsZero() ||
		state.GapCount != 1 || state.LastGapRunID != "run" || state.LastGapRevision != 1 || state.After != "run" {
		t.Fatalf("expired payload was sent or gap not persisted: %+v", state)
	}
	if !worker.Status().Degraded || worker.Status().Gaps != 1 {
		t.Fatal("expired delivery was not reported independently")
	}
}
