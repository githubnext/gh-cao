package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type rebuildDatabase struct {
	dashboardstore.Store
	err error
}

func (s *rebuildDatabase) State(context.Context) (dashboardstore.State, error) {
	return dashboardstore.State{Ready: true, Revision: 4}, s.err
}

type rebuildAuditProbe struct {
	operational.AuditLog
	failEvent string
}

func (s *rebuildAuditProbe) AppendAudit(ctx context.Context, event operational.AuditEvent) (operational.AuditEvent, error) {
	if ctx.Err() != nil {
		return operational.AuditEvent{}, ctx.Err()
	}
	if event.Event == s.failEvent {
		return operational.AuditEvent{}, errors.New("audit unavailable")
	}
	return s.AuditLog.AppendAudit(ctx, event)
}

type rebuildStateProbe struct {
	operational.StateStore
	failAfter int
	writes    int
}

func (s *rebuildStateProbe) SetOperationalState(ctx context.Context, name string, value []byte) error {
	s.writes++
	if s.writes > s.failAfter {
		return errors.New("status unavailable")
	}
	return s.StateStore.SetOperationalState(ctx, name, value)
}

type rebuildFunc func(context.Context) (ingest.Result, error)

func (f rebuildFunc) Rebuild(ctx context.Context) (ingest.Result, error) { return f(ctx) }
func (f rebuildFunc) Reconcile(ctx context.Context, _ GitHubWebhook) (ingest.Result, error) {
	return f(ctx)
}

func newRebuildAuditApp(t *testing.T) (*App, *rebuildAuditProbe) {
	t.Helper()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	auditStore, dsn, namespace := testutil.OperationalPostgres(t)
	path := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(path, []byte(`{"control-plane":{"web":{"host":{"target":{"module":"container"},"operational-store":{"backend":"postgres"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	t.Setenv("CAO_POSTGRES_URL", dsn)
	t.Setenv("CAO_OPERATIONAL_NAMESPACE", namespace)
	probe := &rebuildAuditProbe{AuditLog: auditStore}
	app := &App{
		services: store.OperationalServices(), database: &rebuildDatabase{},
		config: Config{AdminUsers: []string{"operator"}, Audit: audit.New(io.Discard).WithStore(probe)},
		oauth:  &githubOAuth{}, hub: newEventHub(), startContext: t.Context(),
		reconciler: rebuildFunc(func(context.Context) (ingest.Result, error) {
			return ingest.Result{Revision: 5, DataRevision: "snapshot-5"}, nil
		}),
	}
	return app, probe
}

func rebuildRequest(t *testing.T, app *App, session oauthSession) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/admin/rebuild", nil)
	request = request.WithContext(trace.ContextWithSpanContext(request.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	})))
	request = request.WithContext(context.WithValue(request.Context(), oauthSessionContextKey{}, session))
	response := httptest.NewRecorder()
	app.rebuild(response, request)
	if err := app.waitTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	return response
}

func rebuildEvents(t *testing.T, store operational.AuditLog) []operational.AuditEvent {
	t.Helper()
	events, err := store.ListAudit(t.Context(), operational.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRebuildAuditLifecycleIndependentHistory(t *testing.T) {
	app, _ := newRebuildAuditApp(t)
	ids := make([]string, 0, 2)
	for range 2 {
		response := rebuildRequest(t, app, assuredSession("operator"))
		if response.Code != http.StatusAccepted {
			t.Fatalf("rebuild returned %d: %s", response.Code, response.Body.String())
		}
		var admitted rebuildStatus
		if err := json.Unmarshal(response.Body.Bytes(), &admitted); err != nil {
			t.Fatal(err)
		}
		if admitted.RebuildID == "" || admitted.AuditState != "pending" {
			t.Fatalf("missing admission correlation: %+v", admitted)
		}
		ids = append(ids, admitted.RebuildID)
		status, err := app.readRebuildStatus(t.Context())
		if err != nil || status.State != "succeeded" || status.Revision != 5 ||
			status.DataRevision != "snapshot-5" || status.AuditState != "recorded" {
			t.Fatalf("terminal status: %+v, %v", status, err)
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("rebuild correlation was reused")
	}
	// The reader uses a separate read-only connection, not the replaceable status.
	reader, closeReader, err := OpenAuditReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeReader() }()
	events := rebuildEvents(t, reader)
	if len(events) != 6 {
		t.Fatalf("expected both retained lifecycles, got %+v", events)
	}
	for i, event := range events {
		want := []string{"admin.rebuild.admitted", "admin.rebuild.started", "admin.rebuild.completed"}[i%3]
		if event.Event != want || event.Actor != "operator" || event.Outcome != audit.Success ||
			event.Attributes["rebuild_id"] != ids[i/3] || event.Attributes["previous_revision"] != "4" ||
			event.Attributes["started_at"] == "" || event.Attributes["trace_id"] != "01000000000000000000000000000000" {
			t.Fatalf("incorrect lifecycle evidence: %+v", event)
		}
		if i%3 == 2 && (event.Attributes["state"] != "succeeded" || event.Attributes["revision"] != "5" ||
			event.Attributes["data_revision"] != "snapshot-5" || event.Attributes["completed_at"] == "") {
			t.Fatalf("missing terminal evidence: %+v", event)
		}
	}
	if verified, err := reader.VerifyAudit(t.Context()); err != nil || verified.Events != 6 {
		t.Fatalf("independent chain verification: %+v, %v", verified, err)
	}
}

func TestRebuildAuditDeniedAndUnavailableAdmission(t *testing.T) {
	for _, test := range []string{"not_admin", "step_up", "denial_outage", "admission_outage", "missing_durable", "database_outage", "status_outage", "draining"} {
		t.Run(test, func(t *testing.T) {
			app, probe := newRebuildAuditApp(t)
			calls := 0
			app.reconciler = rebuildFunc(func(context.Context) (ingest.Result, error) {
				calls++
				return ingest.Result{}, nil
			})
			session := assuredSession("operator")
			want := http.StatusServiceUnavailable
			switch test {
			case "not_admin":
				session.Login = "reader"
				want = http.StatusForbidden
			case "step_up":
				session.AuthenticatedAt = session.AuthenticatedAt.Add(-projectionLockTTL)
				want = http.StatusUnauthorized
			case "denial_outage":
				session.Login = "reader"
				probe.failEvent = "admin.denied"
			case "admission_outage":
				probe.failEvent = "admin.rebuild.admitted"
			case "missing_durable":
				app.config.Audit = audit.New(io.Discard)
			case "database_outage":
				app.database = &rebuildDatabase{err: errors.New("database unavailable")}
			case "status_outage":
				app.services.State = &rebuildStateProbe{StateStore: app.services.State}
			case "draining":
				app.draining = true
			}
			response := rebuildRequest(t, app, session)
			if response.Code != want || calls != 0 {
				t.Fatalf("rebuild returned %d (want %d), calls=%d: %s", response.Code, want, calls, response.Body.String())
			}
			held, err := app.services.Leases.LockHeld(t.Context(), app.rebuildLockName())
			if err != nil || held {
				t.Fatalf("admission leaked lease: held=%v err=%v", held, err)
			}
			events := rebuildEvents(t, probe)
			switch test {
			case "not_admin", "step_up":
				if len(events) != 1 || events[0].Event != "admin.denied" ||
					events[0].Actor != session.Login || events[0].Outcome != audit.Denied ||
					events[0].Attributes["trace_id"] != "01000000000000000000000000000000" {
					t.Fatalf("missing denial: %+v", events)
				}
			case "status_outage", "draining":
				if len(events) < 2 || events[1].Event != "admin.rebuild.completed" ||
					events[1].Attributes["state"] != "failed" || events[1].Actor != "operator" {
					t.Fatalf("missing failed admission completion: %+v", events)
				}
			default:
				if len(events) != 0 {
					t.Fatalf("unexpected admitted work: %+v", events)
				}
			}
		})
	}
}

func TestRebuildAuditTerminalFailures(t *testing.T) {
	for _, test := range []string{"failure", "canceled", "timeout", "start_outage", "terminal_outage", "status_outage", "mirror_outage", "joined_outage"} {
		t.Run(test, func(t *testing.T) {
			app, probe := newRebuildAuditApp(t)
			runCtx, cancelRun := context.WithCancel(t.Context())
			defer cancelRun()
			app.startContext = runCtx
			wantState, wantAudit, wantCalls := "succeeded", "recorded", 1
			var resultErr error
			switch test {
			case "failure":
				resultErr = errors.New("sensitive source payload must not enter evidence")
				wantState = "failed"
			case "canceled":
				resultErr = context.Canceled
				wantState = "canceled"
			case "timeout":
				resultErr = context.DeadlineExceeded
				wantState = "canceled"
			case "start_outage":
				probe.failEvent = "admin.rebuild.started"
				wantState, wantCalls = "failed", 0
			case "terminal_outage":
				probe.failEvent = "admin.rebuild.completed"
				wantAudit = "unavailable"
			case "status_outage":
				app.services.State = &rebuildStateProbe{StateStore: app.services.State, failAfter: 1}
			case "mirror_outage":
				app.config.Audit = audit.New(failingHostedMirror{}).WithStore(probe)
			case "joined_outage":
				// Both failures must never be mistaken for a mirror-only failure.
				app.config.Audit = audit.New(failingHostedMirror{}).WithStore(probe)
				probe.failEvent = "admin.rebuild.completed"
				wantAudit = "unavailable"
			}
			calls := 0
			app.reconciler = rebuildFunc(func(ctx context.Context) (ingest.Result, error) {
				calls++
				if test == "canceled" {
					cancelRun()
					<-ctx.Done()
					return ingest.Result{}, ctx.Err()
				}
				return ingest.Result{Revision: 5, DataRevision: "snapshot-5"}, resultErr
			})
			response := rebuildRequest(t, app, assuredSession("operator"))
			if response.Code != http.StatusAccepted || calls != wantCalls {
				t.Fatalf("unexpected admission/calls: %d, %d", response.Code, calls)
			}
			status, err := app.readRebuildStatus(t.Context())
			if test == "status_outage" {
				if err != nil || status.State != "interrupted" || status.AuditState != "pending" {
					t.Fatalf("stale status concealed incomplete persistence: %+v, %v", status, err)
				}
			} else if err != nil || status.State != wantState || status.AuditState != wantAudit {
				t.Fatalf("terminal status: %+v, %v", status, err)
			}
			events := rebuildEvents(t, probe)
			var completed, statusFailed bool
			for _, event := range events {
				if event.Actor != "operator" || event.Attributes["rebuild_id"] == "" {
					t.Fatalf("lost terminal attribution: %+v", event)
				}
				for _, value := range event.Attributes {
					if strings.Contains(value, "sensitive") {
						t.Fatal("raw reconciliation error leaked into audit")
					}
				}
				if event.Event == "admin.rebuild.completed" {
					completed = true
					wantOutcome := audit.Failure
					if wantState == "succeeded" {
						wantOutcome = audit.Success
					}
					if event.Outcome != wantOutcome || event.Attributes["state"] != wantState {
						t.Fatalf("incorrect terminal outcome: %+v", event)
					}
				}
				statusFailed = statusFailed || event.Event == "admin.rebuild.status_failed"
			}
			if completed != (wantAudit == "recorded") || statusFailed != (test == "status_outage") {
				t.Fatalf("incorrect independent terminal evidence: %+v", events)
			}
			if wantState == "succeeded" && app.hub.current.revision != 5 {
				t.Fatal("evidence outage suppressed notification of published data")
			}
			held, err := app.services.Leases.LockHeld(t.Context(), app.rebuildLockName())
			if err != nil || held {
				t.Fatalf("terminal failure leaked lease: %v, %v", held, err)
			}
		})
	}
}

func TestRebuildLocalCapabilityCompatibility(t *testing.T) {
	app, _ := newRebuildAuditApp(t)
	app.oauth = nil
	var mirror strings.Builder
	app.config.Audit = audit.New(&mirror)
	response := rebuildRequest(t, app, oauthSession{})
	status, err := app.readRebuildStatus(t.Context())
	if response.Code != http.StatusAccepted || err != nil || status.State != "succeeded" || status.AuditState != "local" {
		t.Fatalf("local rebuild: %d, %+v, %v", response.Code, status, err)
	}

	if !strings.Contains(mirror.String(), `"actor":"local-bearer"`) ||
		strings.Contains(mirror.String(), `"stored":true`) {
		t.Fatalf("local capability falsely claimed personal/durable evidence: %s", mirror.String())
	}
}

func TestRebuildPostgresAuditIndependentHistory(t *testing.T) {
	f := newPostgresRoles(t)
	store, err := postgres.Open(t.Context(), f.runtimeDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	app, _ := newRebuildAuditApp(t)
	app.services = store.OperationalServices()
	app.config.Audit = audit.New(io.Discard).WithStore(app.services.Audit)
	response := rebuildRequest(t, app, assuredSession("operator"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("PostgreSQL runtime rebuild: %d: %s", response.Code, response.Body.String())
	}
	events := rebuildEvents(t, app.services.Audit)
	if len(events) != 3 || events[2].Event != "admin.rebuild.completed" ||
		events[2].Actor != "operator" || events[2].Attributes["revision"] != "5" {
		t.Fatalf("runtime lifecycle evidence: %+v", events)
	}
	if err := app.writeRebuildStatus(t.Context(), rebuildStatus{State: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	testAuditReader(t, f)
}
