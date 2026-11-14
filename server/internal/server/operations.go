package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/githubwebhook"
	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var operationsLog = logger.New("cao:server:operations")

const (
	projectionLockTTL = 30 * time.Minute
	projectionTimeout = 25 * time.Minute
	deliveryTTL       = 7 * 24 * time.Hour
)

type Reconciler interface {
	Rebuild(context.Context) (ingest.Result, error)
	Reconcile(context.Context, GitHubWebhook) (ingest.Result, error)
}

// GitHubWebhook is one verified delivery passed to webhook listeners.
type GitHubWebhook = githubwebhook.Delivery

type DirectoryReconciler struct {
	Store               *postgresx.Store
	SourceDirectory     string
	DatabaseQueriesPath string
}

func (reconciler DirectoryReconciler) Rebuild(ctx context.Context) (ingest.Result, error) {
	return ingest.Run(ctx, reconciler.Store, reconciler.SourceDirectory, ingest.Options{
		DatabaseQueriesPath: reconciler.DatabaseQueriesPath,
		Force:               true,
	})
}

func (reconciler DirectoryReconciler) Reconcile(ctx context.Context, _ GitHubWebhook) (ingest.Result, error) {
	return reconciler.Rebuild(ctx)
}

type rebuildStatus struct {
	State        string         `json:"state"`
	Required     bool           `json:"required"`
	RebuildID    string         `json:"rebuildId,omitempty"`
	AuditState   string         `json:"auditState,omitempty"`
	StartedAt    string         `json:"startedAt,omitempty"`
	CompletedAt  string         `json:"completedAt,omitempty"`
	Revision     int64          `json:"revision,omitempty"`
	DataRevision string         `json:"dataRevision,omitempty"`
	Counts       map[string]int `json:"counts,omitempty"`
}

type rebuildEvidence struct {
	ID               string
	Actor            string
	PreviousRevision int64
}

func (a *App) rebuild(response http.ResponseWriter, request *http.Request) {
	if !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "administrative authorization is required")
		return
	}
	if a.reconciler == nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "rebuild is not configured")
		return
	}
	if a.oauth != nil && !a.config.Audit.Durable() {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "rebuild requires durable audit storage")
		return
	}
	id, err := operationToken()
	if err != nil {
		writeError(request.Context(), response, http.StatusInternalServerError, "rebuild could not start")
		return
	}
	token, err := operationToken()
	if err != nil {
		writeError(request.Context(), response, http.StatusInternalServerError, "rebuild could not start")
		return
	}
	acquired, err := a.services.Leases.TryLock(request.Context(), a.rebuildLockName(), token, projectionLockTTL)
	if err != nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "rebuild coordination is unavailable")
		return
	}
	if !acquired {
		writeError(request.Context(), response, http.StatusConflict, "a projection update is already running")
		return
	}
	started := time.Now().UTC()
	active, err := a.database.State(request.Context())
	if err != nil {
		a.releaseRebuildLock(request.Context(), token)
		writeError(request.Context(), response, http.StatusServiceUnavailable, "rebuild database state is unavailable")
		return
	}
	evidence := rebuildEvidence{ID: id, Actor: "local-bearer", PreviousRevision: active.Revision}
	if a.oauth != nil {
		session, _ := request.Context().Value(oauthSessionContextKey{}).(oauthSession)
		evidence.Actor = session.Login
	}
	status := rebuildStatus{
		State: "running", Required: !active.Ready,
		StartedAt: started.Format(time.RFC3339Nano), RebuildID: id, AuditState: "pending",
	}
	if err := a.recordRebuildEvent(request.Context(), "admin.rebuild.admitted", audit.Success, evidence, status, ""); err != nil {
		a.releaseRebuildLock(request.Context(), token)
		writeError(request.Context(), response, http.StatusServiceUnavailable, "rebuild audit is unavailable")
		return
	}
	if err := a.writeRebuildStatus(request.Context(), status); err != nil {
		status.State = "failed"
		a.finishRebuild(request.Context(), evidence, status, "status_unavailable")
		a.releaseRebuildLock(request.Context(), token)
		writeError(request.Context(), response, http.StatusServiceUnavailable, "rebuild status is unavailable")
		return
	}
	ctx, cancel := a.operationContext(request.Context())
	if !a.launchTask(func() { a.performRebuild(ctx, cancel, token, evidence, status) }) {
		cancel()
		status.State = "failed"
		a.finishRebuild(ctx, evidence, status, "service_stopped")
		a.releaseRebuildLock(request.Context(), token)
		writeError(request.Context(), response, http.StatusServiceUnavailable, "dashboard service is not running")
		return
	}
	writeJSON(response, http.StatusAccepted, status)
}

func (a *App) performRebuild(ctx context.Context, cancel context.CancelFunc, token string, evidence rebuildEvidence, status rebuildStatus) {
	defer cancel()
	defer a.releaseRebuildLock(ctx, token)
	if err := a.recordRebuildEvent(ctx, "admin.rebuild.started", audit.Success, evidence, status, ""); err != nil {
		status.State = "failed"
		a.finishRebuild(ctx, evidence, status, "audit_unavailable")
		return
	}
	if ctx.Err() != nil {
		status.State = "canceled"
		a.finishRebuild(ctx, evidence, status, "operation_canceled")
		return
	}
	result, err := a.reconciler.Rebuild(ctx)
	if err != nil {
		status.State = "failed"
		reason := "reconciliation_failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status.State = "canceled"
			reason = "operation_canceled"
		}
		a.finishRebuild(ctx, evidence, status, reason)
		serverLog.PrintfContext(ctx, "rebuild failed rebuild_id=%s state=%s", evidence.ID, status.State)
		return
	}
	status.State = "succeeded"
	status.Required = false
	status.Revision = result.Revision
	status.DataRevision = result.DataRevision
	status.Counts = result.Counts
	status = a.finishRebuild(ctx, evidence, status, "")
	a.hub.Broadcast(result.Revision)
	serverLog.PrintfContext(ctx, "rebuild published rebuild_id=%s revision=%d audit_state=%s", evidence.ID, result.Revision, status.AuditState)
}

func (a *App) recordRebuildEvent(ctx context.Context, event, outcome string, evidence rebuildEvidence, status rebuildStatus, reason string) error {
	err := a.config.Audit.Record(ctx, event, outcome,
		slog.String("actor", evidence.Actor), slog.String("rebuild_id", evidence.ID),
		slog.Int64("previous_revision", evidence.PreviousRevision),
		slog.String("state", status.State), slog.String("started_at", status.StartedAt),
		slog.String("completed_at", status.CompletedAt), slog.String("reason", reason),
		slog.Int64("revision", status.Revision), slog.String("data_revision", status.DataRevision))
	if err != nil {
		serverLog.PrintfContext(ctx, "rebuild audit write failed rebuild_id=%s event=%s", evidence.ID, event)
		// A mirror-only failure cannot invalidate confirmed durable evidence.
		if a.config.Audit.Durable() && errors.Is(err, audit.ErrMirrorUnavailable) && !errors.Is(err, audit.ErrUnavailable) {
			return nil
		}
	}
	return err
}

func (a *App) finishRebuild(parent context.Context, evidence rebuildEvidence, status rebuildStatus, reason string) rebuildStatus {
	status.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	outcome := audit.Failure
	if status.State == "succeeded" {
		outcome = audit.Success
	}
	if err := a.recordRebuildEvent(parent, "admin.rebuild.completed", outcome, evidence, status, reason); err != nil {
		status.AuditState = "unavailable"
	} else if a.config.Audit.Durable() {
		status.AuditState = "recorded"
	} else {
		status.AuditState = "local"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
	defer cancel()
	if err := a.writeRebuildStatus(ctx, status); err != nil {
		serverLog.PrintfContext(parent, "rebuild terminal status write failed rebuild_id=%s state=%s", evidence.ID, status.State)
		if err := a.recordRebuildEvent(parent, "admin.rebuild.status_failed", audit.Failure, evidence, status, "status_unavailable"); err != nil {
			serverLog.PrintfContext(parent, "rebuild terminal evidence incomplete rebuild_id=%s", evidence.ID)
		}
	}
	return status
}

func (a *App) releaseProjectionLock(parent context.Context, token string) {
	a.releaseLock(parent, "projection", token)
}

func (a *App) rebuildLockName() string {
	if a.Collector() != nil {
		// Native backfill coordinates publications independently of snapshot replacement.
		return "collect:admin-rebuild"
	}
	return "projection"
}

func (a *App) releaseRebuildLock(parent context.Context, token string) {
	a.releaseLock(parent, a.rebuildLockName(), token)
}

func (a *App) releaseLock(parent context.Context, name, token string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
	defer cancel()
	_ = a.services.Leases.Unlock(ctx, name, token)
}

func (a *App) rebuildStatus(response http.ResponseWriter, request *http.Request) {
	status, err := a.readRebuildStatus(request.Context())
	if err != nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "rebuild status is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (a *App) githubWebhook(response http.ResponseWriter, request *http.Request) {
	a.webhookMetrics.begin()
	tracked := &webhookResponseWriter{ResponseWriter: response}
	response = tracked
	ping := false
	defer func() { a.webhookMetrics.finish(tracked.status, ping) }()
	service := a.Webhooks()
	if len(a.webhookSecret) == 0 || len(service.Listeners()) == 0 {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "webhook reconciliation is not configured")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 10<<20)
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		writeError(request.Context(), response, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	if !validWebhookSignature(payload, request.Header.Get("X-Hub-Signature-256"), a.webhookSecret) {
		a.logAuthBranch(request.Context(), "webhook.signature_rejected")
		writeError(request.Context(), response, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	a.logAuthBranch(request.Context(), "webhook.signature_accepted")
	a.webhookMetrics.verified()
	delivery := strings.TrimSpace(request.Header.Get("X-GitHub-Delivery"))
	event := strings.TrimSpace(request.Header.Get("X-GitHub-Event"))
	if delivery == "" || event == "" {
		writeError(request.Context(), response, http.StatusBadRequest, "GitHub delivery and event headers are required")
		return
	}
	ping = event == "ping"
	a.recordIngestionCounter(request.Context(), "webhookReceived")
	a.recordIngestionEvent(request.Context(), "webhook", "")
	outcome, err := service.Dispatch(request.Context(), GitHubWebhook{Delivery: delivery, Event: event, Payload: payload})
	if err != nil {
		var rejection *webhookRejection
		if errors.As(err, &rejection) {
			writeError(request.Context(), response, rejection.status, rejection.message)
			return
		}
		a.recordIngestionCounter(request.Context(), "webhookAdmissionFailed")
		a.recordIngestionFailure(request.Context(), "admission")
		serverLog.PrintfContext(request.Context(), "webhook admission failed")
		writeError(request.Context(), response, http.StatusServiceUnavailable, "webhook admission is unavailable")
		return
	}
	acknowledgement := map[string]any{"accepted": true}
	for key, value := range outcome.Fields {
		acknowledgement[key] = value
	}
	if outcome.Duplicate {
		acknowledgement["duplicate"] = true
		a.webhookMetrics.duplicate()
		a.recordIngestionCounter(request.Context(), "webhookDuplicate")
		serverLog.PrintfContext(request.Context(), "webhook delivery duplicate")
	} else {
		serverLog.PrintfContext(request.Context(), "webhook admission completed kind=%v queued=%v reason=%v admitted_at=%s",
			outcome.Fields["kind"], outcome.Fields["queued"], outcome.Fields["reason"],
			time.Now().UTC().Format(time.RFC3339Nano))
	}
	writeJSON(response, http.StatusAccepted, acknowledgement)
}

// EventAdmitter is implemented by a reconciler that admits webhook deliveries
// without taking the global projection lease.
//
// The directory reconciler does not implement it, so the default profile keeps
// its existing lease-per-delivery behavior unchanged.
type EventAdmitter interface {
	Admit(ctx context.Context, event GitHubWebhook) (map[string]any, error)
}

// reconcilerWebhookListenerName names the built-in listener that forwards
// deliveries to the configured Reconciler.
const reconcilerWebhookListenerName = "reconciler"

// webhookRejection is a listener failure with a specific ingress response.
type webhookRejection struct {
	status  int
	message string
}

func (r *webhookRejection) Error() string { return r.message }

// Webhooks returns the pluggable GitHub webhook service. Components may
// register additional listeners; each receives every verified delivery.
func (a *App) Webhooks() githubwebhook.Service {
	a.webhookOnce.Do(func() {
		if a.webhooks != nil {
			return
		}
		// Apps assembled without New still expose their reconciler.
		registry := githubwebhook.NewRegistry()
		if a.reconciler != nil {
			_, _ = registry.Register(reconcilerWebhookListenerName, a.reconcilerWebhookListener(a.reconciler))
		}
		a.webhooks = registry
	})
	return a.webhooks
}

// reconcilerWebhookListener adapts a Reconciler to the webhook service. An
// EventAdmitter commits deduplication with its durable admission state;
// other reconcilers take the projection lease per delivery.
func (a *App) reconcilerWebhookListener(reconciler Reconciler) githubwebhook.Listener {
	if admitter, ok := reconciler.(EventAdmitter); ok {
		return githubwebhook.ListenerFunc(func(ctx context.Context, event GitHubWebhook) (githubwebhook.Outcome, error) {
			result, err := admitter.Admit(ctx, event)
			if err != nil {
				return githubwebhook.Outcome{}, err
			}
			duplicate, _ := result["duplicate"].(bool)
			return githubwebhook.Outcome{Duplicate: duplicate, Fields: result}, nil
		})
	}
	return githubwebhook.ListenerFunc(func(ctx context.Context, event GitHubWebhook) (githubwebhook.Outcome, error) {
		return a.scheduleReconciliation(ctx, reconciler, event)
	})
}

func (a *App) scheduleReconciliation(ctx context.Context, reconciler Reconciler, event GitHubWebhook) (githubwebhook.Outcome, error) {
	token, err := operationToken()
	if err != nil {
		return githubwebhook.Outcome{}, &webhookRejection{http.StatusInternalServerError, "reconciliation could not start"}
	}
	acquired, err := a.services.Leases.TryLock(ctx, "projection", token, projectionLockTTL)
	if err != nil {
		return githubwebhook.Outcome{}, &webhookRejection{http.StatusServiceUnavailable, "reconciliation coordination is unavailable"}
	}
	if !acquired {
		return githubwebhook.Outcome{}, &webhookRejection{http.StatusConflict, "a projection update is already running"}
	}
	fresh, err := a.services.Deliveries.RememberDelivery(ctx, event.Delivery, deliveryTTL)
	if err != nil {
		a.recordIngestionCounter(ctx, "webhookAdmissionFailed")
		a.recordIngestionFailure(ctx, "redis")
		a.releaseProjectionLock(ctx, token)
		return githubwebhook.Outcome{}, &webhookRejection{http.StatusServiceUnavailable, "webhook deduplication is unavailable"}
	}
	if !fresh {
		a.releaseProjectionLock(ctx, token)
		return githubwebhook.Outcome{Duplicate: true}, nil
	}
	operationCtx, cancel := a.operationContext(ctx)
	if !a.launchTask(func() { a.performReconciliation(operationCtx, cancel, token, reconciler, event) }) {
		cancel()
		a.releaseProjectionLock(ctx, token)
		return githubwebhook.Outcome{}, &webhookRejection{http.StatusServiceUnavailable, "dashboard service is not running"}
	}
	return githubwebhook.Outcome{}, nil
}

func (a *App) recordIngestionCounter(ctx context.Context, name string) {
	if err := a.services.IngestionMetrics.IncrementIngestionCounter(ctx, name); err != nil {
		serverLog.PrintfContext(ctx, "ingestion counter update failed")
	}
}

func (a *App) recordIngestionFailure(ctx context.Context, code string) {
	a.recordIngestionEvent(ctx, "failure", code)
}

func (a *App) recordIngestionEvent(ctx context.Context, event, code string) {
	if err := a.services.IngestionMetrics.RecordIngestionHealthEvent(ctx, event, code, time.Now().UTC()); err != nil {
		serverLog.PrintfContext(ctx, "ingestion health update failed")
	}
}

func (a *App) performReconciliation(
	ctx context.Context,
	cancel context.CancelFunc,
	token string,
	reconciler Reconciler,
	event GitHubWebhook,
) {
	defer cancel()
	defer a.releaseProjectionLock(ctx, token)
	result, err := reconciler.Reconcile(ctx, event)
	if err != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cleanupCancel()
		_ = a.services.Deliveries.ForgetDelivery(cleanupCtx, event.Delivery)
		serverLog.PrintfContext(ctx, "webhook reconciliation failed")
		return
	}
	a.hub.Broadcast(result.Revision)
	serverLog.PrintfContext(ctx, "webhook reconciliation completed revision=%d", result.Revision)
}

func validWebhookSignature(payload []byte, signature string, secret []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return hmac.Equal(provided, mac.Sum(nil))
}

func operationToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (a *App) writeRebuildStatus(ctx context.Context, status rebuildStatus) error {
	payload, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return a.services.State.SetOperationalState(ctx, "rebuild", payload)
}

// reconcileInterruptedRebuild applies the pure decision behind readRebuildStatus's
// "running" branch: a rebuild recorded as running without a held projection lock
// must be reported as interrupted, with Required reflecting whether the database
// is still not ready. completedAt is injected so the decision stays testable
// without depending on wall-clock time.
func reconcileInterruptedRebuild(status rebuildStatus, lockHeld bool, databaseReady bool, completedAt time.Time) rebuildStatus {
	if status.State != "running" || lockHeld {
		return status
	}
	status.State = "interrupted"
	status.Required = !databaseReady
	status.CompletedAt = completedAt.Format(time.RFC3339Nano)
	return status
}

func (a *App) readRebuildStatus(ctx context.Context) (rebuildStatus, error) {
	payload, err := a.services.State.OperationalState(ctx, "rebuild")
	if err != nil {
		return rebuildStatus{}, err
	}
	if len(payload) > 0 {
		var status rebuildStatus
		if err := json.Unmarshal(payload, &status); err != nil {
			return rebuildStatus{}, err
		}
		if status.State == "running" {
			held, err := a.services.Leases.LockHeld(ctx, a.rebuildLockName())
			if err != nil {
				return rebuildStatus{}, err
			}
			if !held {
				active, err := a.database.State(ctx)
				if err != nil {
					return rebuildStatus{}, err
				}
				status = reconcileInterruptedRebuild(status, held, active.Ready, time.Now().UTC())
				operationsLog.PrintfContext(ctx, "rebuild status reconciled as interrupted required=%v", status.Required)
			}
		}
		return status, nil
	}
	active, err := a.database.State(ctx)
	if err != nil {
		return rebuildStatus{}, err
	}
	return rebuildStatus{State: "idle", Required: !active.Ready}, nil
}
