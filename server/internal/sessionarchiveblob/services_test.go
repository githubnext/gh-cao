package sessionarchiveblob

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

type failingBlobService struct {
	BlobStore
	failure error
	calls   int
}

func TestBlobDomainErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		code   string
		exists bool
	}{
		{"conditional create", 412, "ConditionNotMet", true},
		{"Azurite existing immutable blob", 409, "BlobAlreadyExists", true},
		{"lease conflict is not an existing object", 409, "LeaseIdMismatchWithBlobOperation", false},
		{"unknown conflict is not an existing object", 409, "", false},
		{"denial is not an existing object", 403, "AuthorizationFailure", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := &blobResponseError{status: statusError(test.status), code: test.code}
			if errors.Is(err, ErrObjectExists) != test.exists || !isStatus(err, test.status) {
				t.Fatal("Blob error lost its domain meaning or original status")
			}
		})
	}
}

func (s *failingBlobService) CheckPrivate(context.Context) error {
	s.calls++
	return s.failure
}

func TestArchiveServiceInjectionPreservesActivationGates(t *testing.T) {
	capture := nativeCaptureConfig(t)
	destination := validConfig()
	destination.Acceptance = destination.NativeAcceptanceBinding()
	failure := errors.New("synthetic Blob unavailable")
	blob := &failingBlobService{failure: failure}
	queries := &adxQueryClient{}
	for _, test := range []struct {
		name       string
		acceptance string
		services   Services
		want       error
	}{
		{"wrong acceptance before service access", "wrong", Services{Blob: blob, Queries: queries}, ErrConformance},
		{"missing Blob service", destination.Acceptance, Services{Queries: queries}, nil},
		{"missing ADX service", destination.Acceptance, Services{Blob: blob}, nil},
		{"unavailable service without fallback", destination.Acceptance, Services{Blob: blob, Queries: queries}, failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := destination
			config.Acceptance = test.acceptance
			result, err := NewNativeWithServices(t.Context(), capture, config, test.services)
			if err == nil || result != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("expected explicit constructor failure: %v", err)
			}
		})
	}
	if blob.calls != 1 {
		t.Fatalf("invalid activation reached Blob or failure retried a fallback: %d calls", blob.calls)
	}
	disabled, err := NewWithServices(t.Context(), Config{}, nil, Services{Blob: blob})
	if err != nil || disabled.Status().Enabled || blob.calls != 1 {
		t.Fatal("disabled archive attempted service access")
	}
}

func TestArchiveInjectedServicesHTTPIntegration(t *testing.T) {
	protocol := &blobProtocol{objects: map[string][]byte{}, etags: map[string]string{},
		uploads: map[string]int{}, now: time.Now().UTC()}
	server := httptest.NewServer(protocol)
	t.Cleanup(server.Close)
	transport, err := simulator.NewLocalTransport(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, CheckRedirect: rejectRedirect, Timeout: 5 * time.Second}
	config := validConfig()
	queryConfig := config
	queryConfig.Endpoint = server.URL
	services := Services{
		Blob:    &blobClient{root: server.URL + "/container/archive", http: client},
		Queries: &adxQueryClient{config: queryConfig, http: client},
	}
	config.Acceptance = config.NativeAcceptanceBinding()
	archive, err := NewNativeWithServices(t.Context(), nativeCaptureConfig(t), config, services)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := archive.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	// A gap receipt needs no invented ADX event rows. Real content/engine
	// acceptance remains separate from this injected-service recovery case.
	if err := archive.CaptureSession(t.Context(), "octo/api", "123", 1, now, nil, "missing-session"); err == nil ||
		!strings.Contains(err.Error(), "session archive acquisition gap: missing-session") {
		t.Fatalf("gap admission must remain an explicit incomplete result: %v", err)
	}
	before := archive.Status()
	revisions := archive.active.Revisions
	if revisions == 0 || before.Gaps != 1 || before.ActiveTraces != 0 {
		t.Fatalf("injected services did not persist the gap receipt: %+v", before)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewNativeWithServices(t.Context(), nativeCaptureConfig(t), config, services)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if reopened.Status().Generation != before.Generation || reopened.Status().Gaps != 1 || reopened.active.Revisions != revisions {
		t.Fatal("restart did not recover publication through injected services")
	}
	if err := reopened.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if reopened.Status().ActiveTraces != 0 || reopened.Status().Gaps != 0 {
		t.Fatal("withdrawal did not persist through injected Blob service")
	}
}

var _ sessionarchive.Publisher = (*Archive)(nil)
