package server

import (
	"context"
	"errors"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
	"github.com/githubnext/gh-cao/server/internal/sessionarchiveblob"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestSessionArchiveAuthorityConfigurationRejectsUnavailableCanonicalBackend(t *testing.T) {
	for name, app := range map[string]*App{
		"missing":   {},
		"typed nil": {database: (*postgresx.Store)(nil)},
		"unopened":  {database: &postgresx.Store{}},
	} {
		t.Run(name, func(t *testing.T) {
			authority, err := app.nativeSessionArchiveAuthority(t.Context())
			if authority != nil || !errors.Is(err, collectnative.ErrUnsupported) {
				t.Fatal("unavailable canonical storage selected an archive authority")
			}
		})
	}
	probe := &archiveAuthorityProbe{}
	app := &App{config: Config{SessionArchiveAuthority: probe}}
	if authority, err := app.nativeSessionArchiveAuthority(t.Context()); err != nil || authority != probe {
		t.Fatal("explicit archive authority injection was replaced")
	}
}

func TestSessionArchiveAuthorityConfigurationUsesCanonicalPostgres(t *testing.T) {
	database := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	app := &App{database: database}
	authority, err := app.nativeSessionArchiveAuthority(t.Context())
	if err != nil || authority == nil {
		t.Fatalf("canonical PostgreSQL did not supply the production archive authority: %v", err)
	}
	// No enrollment/registration was created by resolving configuration.
	if _, err := authority.Current(t.Context(), "reviewed-native-binding", "octo/api"); !errors.Is(err, sessionarchive.ErrWithdrawn) {
		t.Fatalf("production authority did not enforce unregistered source visibility: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if authority, err := app.nativeSessionArchiveAuthority(t.Context()); err == nil || authority != nil {
		t.Fatal("closed canonical PostgreSQL selected a production archive authority")
	}
}

// This models the domain contract to test server call ordering. It is not a
// PostgreSQL adapter or evidence of enrollment transaction/row-lock conformance.
type archiveAuthorityProbe struct {
	source          sessionarchive.Source
	registerError   error
	currentError    error
	loseReadContext bool
	registered      bool
}

func (a *archiveAuthorityProbe) Register(_ context.Context, _ string, source sessionarchive.Source) error {
	if a.registerError != nil {
		return a.registerError
	}
	if source != a.source {
		return sessionarchive.ErrWithdrawn
	}
	a.registered = true
	return nil
}

func (a *archiveAuthorityProbe) Current(context.Context, string, string) (sessionarchive.Source, error) {
	if a.currentError != nil {
		return sessionarchive.Source{}, a.currentError
	}
	if !a.registered {
		return sessionarchive.Source{}, sessionarchive.ErrWithdrawn
	}
	return a.source, nil
}

func (a *archiveAuthorityProbe) WithRead(ctx context.Context, destination string, source sessionarchive.Source, action func(context.Context) error) error {
	current, err := a.Current(ctx, destination, source.Repository)
	if err != nil {
		return err
	}
	if current != source {
		return sessionarchive.ErrWithdrawn
	}
	if a.loseReadContext {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return action(ctx)
	}
	return action(ctx)
}

func (*archiveAuthorityProbe) DrainWithdrawals(context.Context, string, int, func(context.Context, sessionarchive.Source) error) (int, error) {
	return 0, nil
}

func TestSessionArchiveAuthorityFencesAdmissionBeforeAndAfterBlob(t *testing.T) {
	source := sessionarchive.Source{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	probe := &archiveAuthorityProbe{source: source}
	archive := &nativeSessionArchive{authority: probe, destination: "reviewed-native-binding"}
	unavailable := errors.New("synthetic authority unavailable")
	probe.registerError = unavailable
	called := false
	if err := archive.admit(t.Context(), source, func(context.Context) error { called = true; return nil }); !errors.Is(err, unavailable) || called {
		t.Fatal("Blob capture ran without a durably registered source")
	}
	probe.registerError = nil
	if err := archive.admit(t.Context(), source, func(context.Context) error {
		if !probe.registered {
			t.Fatal("Blob write preceded durable source registration")
		}
		// Enrollment committed during remote publication; the Blob write may
		// finish, but acquisition must not acknowledge the revoked source.
		probe.currentError = sessionarchive.ErrWithdrawn
		return nil
	}); !errors.Is(err, sessionarchive.ErrWithdrawn) {
		t.Fatal("a stale collector acknowledged after enrollment withdrawal")
	}
	probe.currentError = nil
	probe.source.Epoch = 3
	if err := archive.admit(t.Context(), source, func(context.Context) error { t.Fatal("stale dispatch wrote Blob"); return nil }); !errors.Is(err, sessionarchive.ErrWithdrawn) {
		t.Fatal("old dispatched epoch was silently replaced by reenrollment")
	}
}

func TestSessionArchiveAuthorityRevokesPinnedResultsBeforeBlobCleanup(t *testing.T) {
	source := sessionarchive.Source{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	probe := &archiveAuthorityProbe{source: source, registered: true}
	inner := &sessionArchiveReadProbe{}
	reader := &authorizedArchiveReader{inner: inner, authority: probe, destination: "reviewed-native-binding", source: source, ctx: t.Context()}
	delivered := false
	deliver := func() error { delivered = true; return nil }
	probe.currentError = sessionarchive.ErrWithdrawn
	if err := reader.Deliver(deliver); !errors.Is(err, sessionarchive.ErrWithdrawn) || delivered {
		t.Fatal("withdrawal left a pinned Blob result readable while cleanup was pending")
	}
	probe.currentError = nil
	probe.source.Epoch = 3
	if err := reader.Deliver(deliver); !errors.Is(err, sessionarchive.ErrWithdrawn) || delivered {
		t.Fatal("reenrollment reauthorized a previous epoch's pinned result")
	}
	probe.source = source
	probe.loseReadContext = true
	if err := reader.Deliver(deliver); !errors.Is(err, context.Canceled) || delivered {
		t.Fatal("lost authority transaction still delivered approved content")
	}
	probe.loseReadContext = false
	if err := reader.Deliver(deliver); err != nil || !delivered {
		t.Fatal("current registered source could not deliver an authorized result")
	}
	if err := reader.Close(); err != nil || inner.closed != 1 {
		t.Fatal("authority guard lost the Blob reader lifetime")
	}
}

func TestSessionArchiveRequiresDurableAuthorityAndFrozenNativeDestination(t *testing.T) {
	if _, err := openNativeSessionArchive(t.Context(), sessionarchive.Config{}, sessionarchiveblob.Config{}, nil); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatal("startup without an enrollment-atomic ledger/outbox selected a fallback")
	}
	source := sessionarchive.Source{Repository: "octo/api", InstallationID: 7, Epoch: 1}
	probe := &archiveAuthorityProbe{source: source}
	service := &sessionArchiveService{backend: &nativeSessionArchive{authority: probe}, repositories: []string{"octo/api"}}
	destination, err := service.NativeDestination(collectnative.EnrollmentScope{Repository: source.Repository, InstallationID: 7, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := destination.Capture(t.Context(), collectnative.Run{Identity: collectnative.Identity{
		Repository: source.Repository, InstallationID: 8, RunID: 123, Attempt: 1,
	}}, nil, "missing-session"); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatal("destination accepted a run from a different dispatched installation")
	}
	if err := service.WithdrawRepository(t.Context(), source.Repository); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatal("bare repository callback replaced the enrollment-atomic outbox")
	}
}
