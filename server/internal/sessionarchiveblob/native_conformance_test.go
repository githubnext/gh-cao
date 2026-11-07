package sessionarchiveblob

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

// This opt-in gate needs a dedicated real Azure destination. Protocol fixtures
// cannot produce the native acceptance binding used to activate a deployment.
func TestAzureNativeArchiveConformance(t *testing.T) {
	if os.Getenv("CAO_SESSION_ARCHIVE_REQUIRE_AZURE_NATIVE") != "1" {
		t.Skip("native Azure acceptance unavailable; require with CAO_SESSION_ARCHIVE_REQUIRE_AZURE_NATIVE=1")
	}
	if os.Getenv("CAO_SESSION_ARCHIVE_AZURE_ALLOW_WRITES") != "1" {
		t.Fatal("explicit dedicated-destination conformance write approval is required")
	}
	destination, err := FromEnv(os.Getenv)
	if err != nil || !destination.Enabled || !strings.HasPrefix(destination.Prefix, "conformance-") ||
		len(destination.RepositoryIDs) != 1 || destination.RetirementGrace != 5*time.Minute {
		t.Fatal("requires empty dedicated conformance-* Blob destination, provisioned ADX table, one synthetic repository and 5m retirement grace")
	}
	destination.Acceptance = destination.NativeAcceptanceBinding()
	capture := nativeCaptureConfig(t)
	capture.Namespace, capture.Repositories = destination.Namespace, destination.RepositoryIDs
	n, err := NewNative(t.Context(), capture, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = n.Close() }()
	if n.active.Revisions != 0 || n.active.Generation != 0 {
		t.Fatal("native conformance destination must be empty; never reuse production or legacy state")
	}
	competing, err := NewNative(t.Context(), capture, destination)
	if err == nil {
		_ = competing.Close()
		t.Fatal("native overlapping owner acquired the same lease")
	}
	if !errors.Is(err, ErrLease) {
		t.Fatalf("unexpected owner-overlap result: %v", err)
	}
	repository := destination.RepositoryIDs[0]
	completed := time.Now().UTC().Add(-time.Hour)
	source := nativeSource("synthetic original")
	if err := n.CaptureSession(t.Context(), repository, "123", 2, completed, source, ""); err != nil {
		t.Fatal(err)
	}
	originalFile := n.state.Manifest.Files[0]
	generation := n.Status().Generation
	if err := n.CaptureSession(t.Context(), repository, "123", 2, completed, source, ""); err != nil || n.Status().Generation != generation {
		t.Fatal("native immutable admission retry was not idempotent")
	}
	expected, err := sessionarchive.Project(n.traceConfig(), capture.Namespace, repository, "123",
		completed.Format(time.DateOnly), 2, source, n.state.NativeRecords[0].CapturedAt)
	if err != nil {
		t.Fatal(err)
	}
	scope := sessionarchive.Scope{Namespace: capture.Namespace, Repositories: capture.Repositories}
	_, rows, err := n.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: repository, RunID: "123", Attempt: 2, Limit: 100})
	if err != nil || !reflect.DeepEqual(rows, expected.Events) {
		t.Fatal("real ADX could not reconstruct complete Go-generated Parquet")
	}
	oldCtx, _, _, _, oldReader, err := n.pin(t.Context(), scope, repository, "123", 2)
	if err != nil {
		t.Fatal(err)
	}
	corrected := nativeSource("synthetic corrected")
	if err := n.CaptureSession(t.Context(), repository, "123", 2, completed, corrected, ""); err != nil {
		t.Fatal(err)
	}
	if oldCtx.Err() != nil || oldReader.Deliver(func() error { return nil }) != nil {
		t.Fatal("native ordinary correction revoked a pinned generation")
	}
	_ = oldReader.Close()
	if err := n.CaptureSession(t.Context(), repository, "123", 2, completed, source, ""); !errors.Is(err, sessionarchive.ErrReplay) {
		t.Fatal("native correction lost supersession fence")
	}
	_, summaries, err := n.Summaries(t.Context(), scope, repository, 100)
	if err != nil || len(summaries) != 1 || summaries[0].Events != 3 || summaries[0].Attempt != 2 {
		t.Fatal("real native external-table membership inflated or changed attempt identity")
	}
	withdrawCtx, _, _, _, reader, err := n.pin(t.Context(), scope, repository, "123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Withdraw(t.Context(), repository, "123", 2); err != nil {
		t.Fatal(err)
	}
	if withdrawCtx.Err() == nil || !errors.Is(reader.Deliver(func() error { return nil }), ErrWithdrawn) {
		t.Fatal("native withdrawal did not override pinned access")
	}
	_ = reader.Close()
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	n, err = NewNative(t.Context(), capture, destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.CaptureSession(t.Context(), repository, "123", 2, completed, corrected, ""); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("native restart lost durable withdrawal")
	}
	if _, _, err := n.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: repository, RunID: "123", Attempt: 2, Limit: 100}); !errors.Is(err, sessionarchive.ErrNotFound) {
		t.Fatal("native withdrawal remained queryable")
	}
	if err := n.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := n.blob.Verify(t.Context(), originalFile.Path, originalFile.Digest, originalFile.Bytes); err != nil {
		t.Fatal("native retirement deleted historical data before conservative grace")
	}
	timer := time.NewTimer(time.Until(n.active.ReadersUntil.Add(destination.RetirementGrace + time.Second)))
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal("native live retirement acceptance requires a timeout of at least 15m")
	case <-timer.C:
	}
	if err := n.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := n.blob.Verify(t.Context(), originalFile.Path, originalFile.Digest, originalFile.Bytes); !isStatus(err, http.StatusNotFound) {
		t.Fatal("native eligible object was not physically retired")
	}
	t.Log("Native Azure destination conformance passed; explicit runtime acceptance binding:", destination.Acceptance)
}
