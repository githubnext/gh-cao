package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var hostedCLILog = logger.New("cao:server:hostedcli")

const maxHostedCLIRequestBytes = 1 << 20

// HostedCLIConfig enables the optional hosted CLI action queue. Commands are
// admitted here with the requester's OAuth identity and executed separately by
// `cao-dashboard execute-cli` with single-use GitHub App tokens.
type HostedCLIConfig struct {
	Catalog *hostedcli.Catalog
	// ControlRepository is the OWNER/REPOSITORY targeted by hosted gh-aw
	// package updates and is rendered as {{cao-repository}}.
	ControlRepository string
	// GitHubURL is the web base used for {{cao-repository-url}}.
	GitHubURL string
	// DashboardURL is the public dashboard URL rendered as {{cao-url}}.
	DashboardURL string
	// Queue overrides the operational queue; tests only.
	Queue operational.CLICommandQueue
	// DebugStore replaces the PostgreSQL queue and audit table with a local
	// single-host store (serve-hosted --hosted-cli-sqlite). It is accepted
	// only on a loopback listener.
	DebugStore HostedCLIDebugStore
}

// HostedCLIDebugStore is a local queue and audit table, such as the SQLite
// store used for debugging.
type HostedCLIDebugStore interface {
	operational.CLICommandQueues
	operational.AuditLog
}

type hostedCLI struct {
	queue    operational.AuditedCLICommandQueue
	catalog  *hostedcli.Catalog
	context  hostedcli.TemplateContext
	control  string
	reposURL string
	local    *LocalOperatorActionsConfig
}

// EnableHostedCLIActions opts this hosted app into admitting dashboard CLI
// actions. It is disabled unless the operator passes --hosted-cli-actions.
func (a *App) EnableHostedCLIActions(ctx context.Context, config HostedCLIConfig) error {
	if a.oauth == nil {
		return errors.New("hosted CLI actions require GitHub OAuth")
	}
	if config.Catalog == nil {
		return errors.New("hosted CLI actions require the dashboard CLI action catalog")
	}
	if !hostedcli.ValidRepository(config.ControlRepository) {
		return errors.New("hosted CLI actions require CAO_HOSTED_CLI_CONTROL_REPOSITORY as OWNER/REPOSITORY")
	}
	templateContext, err := hostedcli.NewTemplateContext(config.ControlRepository, config.GitHubURL, config.DashboardURL)
	if err != nil {
		return err
	}
	queues := a.services.CLICommands
	if config.DebugStore != nil {
		if !isLoopbackListen(a.config.Listen) {
			return errors.New("hosted CLI SQLite debug store requires a loopback listen address")
		}
		queues = config.DebugStore
		a.config.Audit = a.config.Audit.WithStore(config.DebugStore)
		a.config.Logger.Printf("hosted CLI actions use a local SQLite debug store; not for production")
	}
	queue := config.Queue
	if queue == nil {
		if queues == nil {
			return errors.New("hosted CLI actions require the postgres operational store or --hosted-cli-sqlite")
		}
		options, err := hostedCLIQueueOptions(config.DebugStore != nil)
		if err != nil {
			return err
		}
		queue, err = queues.OpenCLICommandQueue(ctx, hostedcli.QueueName, options)
		if err != nil {
			return errors.New("open hosted CLI command queue failed")
		}
	}
	if !a.config.Audit.Durable() {
		return errors.New("hosted CLI actions require the postgres operational audit table or --hosted-cli-sqlite")
	}
	audited, ok := queue.(operational.AuditedCLICommandQueue)
	if !ok {
		return errors.New("hosted CLI actions require atomic queue and audit persistence")
	}
	a.hostedCLI = &hostedCLI{
		queue: audited, catalog: config.Catalog, context: templateContext, control: config.ControlRepository,
		reposURL: strings.TrimSuffix(a.oauth.config.UserURL, "user") + "repos/",
	}
	a.oauth.repositoryConsentAllowed = a.adminListed
	a.config.Logger.Printf("hosted CLI actions enabled control_repository=%s", config.ControlRepository)
	return nil
}

func (a *App) registerHostedCLIRoutes(register func(string, http.HandlerFunc)) {
	if a.hostedCLI == nil {
		return
	}
	register("POST /__cli_commands", a.hostedCLIEnqueue)
	register("GET /__cli_commands", a.hostedCLIList)
	register("GET /__cli_commands/{id}", a.hostedCLIDetail)
	register("GET /__cli_commands/{id}/output", a.hostedCLIOutput)
	register("POST /__cli_commands/{id}/cancel", a.hostedCLICancel)
	register("POST /__cli_commands/{id}/retry", a.hostedCLIRetry)
}

type hostedCLIError struct {
	status  int
	message string
}

func (e *hostedCLIError) Error() string { return e.message }

func cliFailure(status int, message string) error {
	return &hostedCLIError{status: status, message: message}
}

func writeHostedCLIError(ctx context.Context, response http.ResponseWriter, err error) {
	var request *hostedcli.RequestError
	var failure *hostedCLIError
	switch {
	case errors.As(err, &request):
		writeError(ctx, response, request.Status, request.Message)
	case errors.As(err, &failure):
		writeError(ctx, response, failure.status, failure.message)
	case errors.Is(err, operational.ErrCLICommandNotFound):
		writeError(ctx, response, http.StatusNotFound, "CLI command was not found.")
	case errors.Is(err, operational.ErrCLICommandInput):
		writeError(ctx, response, http.StatusBadRequest, "Invalid CLI command request.")
	case errors.Is(err, operational.ErrConflict):
		writeError(ctx, response, http.StatusConflict, "CLI command request conflicts with an existing command.")
	case errors.Is(err, audit.ErrUnavailable):
		hostedCLILog.PrintfContext(ctx, "hosted CLI mutation could not be confirmed: %v", err)
		writeError(ctx, response, http.StatusServiceUnavailable, "The CLI command mutation could not be confirmed. Check the queue before retrying, and reuse the same idempotency key.")
	default:
		hostedCLILog.PrintfContext(ctx, "hosted CLI request failed: %v", err)
		writeError(ctx, response, http.StatusServiceUnavailable, "The hosted CLI command queue is unavailable.")
	}
}

// authorizeHostedCLI requires a configured administrator with a fresh,
// assured OAuth step-up. Every hosted CLI route, including reads, exposes
// operator commands and output.
func (a *App) authorizeHostedCLI(response http.ResponseWriter, request *http.Request) (oauthSession, bool) {
	if a.hostedCLI.local != nil {
		if request.Context().Value(localCanvasContextKey{}) != true ||
			!a.authorized(request) ||
			(request.Method != http.MethodGet && request.Header.Get("Origin") != "http://"+request.Host) {
			writeError(request.Context(), response, http.StatusForbidden, "Local command capability and same-origin approval are required.")
			return oauthSession{}, false
		}
		return oauthSession{Login: "local"}, true
	}
	session, ok := request.Context().Value(oauthSessionContextKey{}).(oauthSession)
	if !ok || !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "Administrator access is required for CLI actions.")
		return oauthSession{}, false
	}
	return session, true
}

// mutateCLICommand commits the queue mutation and assurance event together.
// A timeout can leave the commit outcome unknown, but never split the two.
func (a *App) mutateCLICommand(request *http.Request, session oauthSession, event string,
	mutate func(context.Context, operational.AuditEvent) (operational.CLICommand, operational.AuditEvent, error),
) (operational.CLICommand, error) {
	var command operational.CLICommand
	err := a.config.Audit.RecordAtomic(request.Context(), event, audit.Success,
		func(ctx context.Context, entry operational.AuditEvent) (operational.AuditEvent, error) {
			var err error
			command, entry, err = mutate(ctx, entry)
			return entry, err
		},
		slog.String("actor", session.Login),
		slog.Any("assurance", session.Assurance),
		slog.Time("authenticated_at", session.AuthenticatedAt),
	)
	if errors.Is(err, audit.ErrMirrorUnavailable) && !errors.Is(err, audit.ErrUnavailable) {
		hostedCLILog.PrintfContext(request.Context(), "hosted CLI mutation stored id=%d status=%s but audit mirror unavailable", command.ID, command.Status)
		err = cliFailure(http.StatusServiceUnavailable, fmt.Sprintf(
			"CLI command %d is %s. The mutation and audit event were stored, but the audit mirror is unavailable. Check the queue before retrying, and reuse the same idempotency key.",
			command.ID, command.Status))
	}
	return command, err
}

func (a *App) auditCLIRejected(request *http.Request, session oauthSession, action string, err error) {
	status := http.StatusInternalServerError
	var requestErr *hostedcli.RequestError
	var failure *hostedCLIError
	switch {
	case errors.As(err, &requestErr):
		status = requestErr.Status
	case errors.As(err, &failure):
		status = failure.status
	}
	_ = a.config.Audit.Record(request.Context(), "cli_command.rejected", audit.Denied,
		slog.String("actor", session.Login), slog.String("action", action), slog.Int("http_status", status))
}

func readHostedCLIJSON(request *http.Request, output any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return cliFailure(http.StatusUnsupportedMediaType, "CLI action requests must be JSON.")
	}
	request.Body = http.MaxBytesReader(nil, request.Body, maxHostedCLIRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return cliFailure(http.StatusBadRequest, "Invalid CLI action request.")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return cliFailure(http.StatusBadRequest, "Invalid CLI action request.")
	}
	return nil
}

func idempotencyKey(request *http.Request, body string) string {
	if body != "" {
		return body
	}
	return request.Header.Get("Idempotency-Key")
}

// admit resolves a declared action, plans hosted execution, and checks that
// the requester can write to the single target repository with their own
// OAuth token. The executor re-checks permission before it runs.
func (a *App) admit(ctx context.Context, session oauthSession, request hostedcli.Request) (operational.CLICommandInvocation, error) {
	if a.hostedCLI.local != nil {
		return a.admitOperatorAction(request)
	}
	if session.GitHubID <= 0 || session.Login == "" {
		return operational.CLICommandInvocation{}, cliFailure(http.StatusForbidden, "A verified requester account identity is required. Sign in again before submitting CLI actions.")
	}
	invocation, err := a.hostedCLI.catalog.Resolve(request, a.hostedCLI.context)
	if err != nil {
		return invocation, err
	}
	plan, err := hostedcli.PlanCommand(invocation.Command, a.hostedCLI.control)
	if err != nil {
		return invocation, cliFailure(http.StatusUnprocessableEntity, "This CLI action must run locally: "+err.Error())
	}
	if !a.requesterCanWrite(ctx, session.AccessToken, plan.Repository) {
		return invocation, cliFailure(http.StatusForbidden, "Write access to the target repository is required.")
	}
	invocation.Actor = session.Login
	invocation.ActorID = session.GitHubID
	invocation.Repository = plan.Repository
	return invocation, nil
}

// requireRepositoryWrite authorizes a mutation of an existing command: the
// requester must have write access to its target repository.
func (a *App) requireRepositoryWrite(request *http.Request, session oauthSession, id int64) error {
	command, err := a.hostedCLI.queue.Detail(request.Context(), id)
	if err != nil {
		return err
	}
	if a.hostedCLI.local != nil {
		if !a.hostedCLI.local.allows(command.Repository) {
			return cliFailure(http.StatusForbidden, "The command is outside the selected repository scope.")
		}
		return nil
	}
	if !a.requesterCanWrite(request.Context(), session.AccessToken, command.Repository) {
		a.auditCLIRejected(request, session, command.ActionID, cliFailure(http.StatusForbidden, ""))
		return cliFailure(http.StatusForbidden, "Write access to the target repository is required.")
	}
	return nil
}

func (a *App) requesterCanWrite(ctx context.Context, token, repository string) bool {
	owner, name, _ := strings.Cut(repository, "/")
	var payload struct {
		Permissions struct {
			Admin    bool `json:"admin"`
			Maintain bool `json:"maintain"`
			Push     bool `json:"push"`
		} `json:"permissions"`
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	endpoint := a.hostedCLI.reposURL + url.PathEscape(owner) + "/" + url.PathEscape(name)
	if err := a.oauth.githubJSON(ctx, http.MethodGet, endpoint, token, nil, &payload); err != nil {
		hostedCLILog.PrintfContext(ctx, "requester repository permission lookup failed")
		return false
	}
	return payload.Permissions.Admin || payload.Permissions.Maintain || payload.Permissions.Push
}

func (a *App) hostedCLIEnqueue(response http.ResponseWriter, request *http.Request) {
	session, ok := a.authorizeHostedCLI(response, request)
	if !ok {
		return
	}
	var payload hostedcli.Request
	if err := readHostedCLIJSON(request, &payload); err != nil {
		writeHostedCLIError(request.Context(), response, err)
		return
	}
	invocation, err := a.admit(request.Context(), session, payload)
	if err != nil {
		a.auditCLIRejected(request, session, payload.ID, err)
		writeHostedCLIError(request.Context(), response, err)
		return
	}
	command, err := a.mutateCLICommand(request, session, "cli_command.admitted",
		func(ctx context.Context, event operational.AuditEvent) (operational.CLICommand, operational.AuditEvent, error) {
			return a.hostedCLI.queue.EnqueueAudited(ctx, invocation, idempotencyKey(request, payload.IdempotencyKey), 0, event)
		})
	if err != nil {
		writeHostedCLIError(request.Context(), response, err)
		return
	}
	hostedCLILog.PrintfContext(request.Context(), "hosted CLI command queued id=%d action=%s", command.ID, command.ActionID)
	writeJSON(response, http.StatusAccepted, hostedCLICommandJSON(command))
}

func (a *App) hostedCLIList(response http.ResponseWriter, request *http.Request) {
	if _, ok := a.authorizeHostedCLI(response, request); !ok {
		return
	}
	query := request.URL.Query()
	filter := operational.CLICommandFilter{Limit: 50}
	if value := query.Get("status"); value != "" {
		for _, status := range strings.Split(value, ",") {
			filter.Statuses = append(filter.Statuses, operational.CLICommandStatus(status))
		}
	}
	for name, target := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		if value := query.Get(name); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				writeError(request.Context(), response, http.StatusBadRequest, "Invalid queue pagination.")
				return
			}
			*target = parsed
		}
	}
	if filter.Limit > 300 || filter.Validate() != nil {
		writeError(request.Context(), response, http.StatusBadRequest, "Invalid queue pagination.")
		return
	}
	page, err := a.hostedCLI.queue.List(request.Context(), filter)
	if err != nil {
		writeHostedCLIError(request.Context(), response, err)
		return
	}
	commands := make([]map[string]any, 0, len(page.Commands))
	for _, command := range page.Commands {
		summary := hostedCLICommandJSON(command)
		delete(summary, "arguments")
		delete(summary, "values")
		summary["campaign"] = command.Values["campaign"]
		if summary["repository"] == "" {
			summary["repository"] = command.Values["repository"]
		}
		commands = append(commands, summary)
	}
	var paused any
	if page.RecoveryBlocked {
		paused = "The hosted executor is waiting for operator recovery."
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"commands": commands, "total": page.Total, "running": page.Running,
		"revision": page.Revision, "pausedReason": paused,
	})
}

func commandID(request *http.Request) (int64, error) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, cliFailure(http.StatusNotFound, "CLI command was not found.")
	}
	return id, nil
}

func (a *App) hostedCLIDetail(response http.ResponseWriter, request *http.Request) {
	if _, ok := a.authorizeHostedCLI(response, request); !ok {
		return
	}
	id, err := commandID(request)
	if err == nil {
		var command operational.CLICommand
		if command, err = a.hostedCLI.queue.Detail(request.Context(), id); err == nil {
			writeJSON(response, http.StatusOK, hostedCLICommandJSON(command))
			return
		}
	}
	writeHostedCLIError(request.Context(), response, err)
}

func (a *App) hostedCLICancel(response http.ResponseWriter, request *http.Request) {
	session, ok := a.authorizeHostedCLI(response, request)
	if !ok {
		return
	}
	var payload struct {
		IdempotencyKey string `json:"idempotencyKey,omitempty"`
	}
	id, err := commandID(request)
	if err == nil {
		err = readHostedCLIJSON(request, &payload)
	}
	if err == nil {
		err = a.requireRepositoryWrite(request, session, id)
	}
	if err == nil {
		var command operational.CLICommand
		command, err = a.mutateCLICommand(request, session, "cli_command.cancel_requested",
			func(ctx context.Context, event operational.AuditEvent) (operational.CLICommand, operational.AuditEvent, error) {
				return a.hostedCLI.queue.CancelAudited(ctx, id, event)
			})
		if err == nil {
			hostedCLILog.PrintfContext(request.Context(), "hosted CLI command cancel requested id=%d", id)
			writeJSON(response, http.StatusOK, hostedCLICommandJSON(command))
			return
		}
	}
	writeHostedCLIError(request.Context(), response, err)
}

func (a *App) hostedCLIRetry(response http.ResponseWriter, request *http.Request) {
	session, ok := a.authorizeHostedCLI(response, request)
	if !ok {
		return
	}
	var payload struct {
		IdempotencyKey string `json:"idempotencyKey,omitempty"`
	}
	id, err := commandID(request)
	if err == nil {
		err = readHostedCLIJSON(request, &payload)
	}
	var original operational.CLICommand
	if err == nil {
		original, err = a.hostedCLI.queue.Detail(request.Context(), id)
	}
	if err == nil && original.Status != operational.CLICommandFailed && original.Status != operational.CLICommandTimedOut {
		err = cliFailure(http.StatusConflict, "Only failed or timed-out CLI actions can be retried.")
	}
	var invocation operational.CLICommandInvocation
	if err == nil {
		invocation, err = a.admit(request.Context(), session, hostedcli.Request{
			ID: original.ActionID, Arguments: original.Arguments, Values: original.Values, Input: original.Input,
		})
	}
	if err == nil && invocation.Command != original.Command {
		err = cliFailure(http.StatusConflict, "CLI action declaration has changed.")
	}
	if err == nil {
		var retried operational.CLICommand
		retried, err = a.mutateCLICommand(request, session, "cli_command.retried",
			func(ctx context.Context, event operational.AuditEvent) (operational.CLICommand, operational.AuditEvent, error) {
				return a.hostedCLI.queue.EnqueueAudited(ctx, invocation, idempotencyKey(request, payload.IdempotencyKey), id, event)
			})
		if err == nil {
			writeJSON(response, http.StatusAccepted, hostedCLICommandJSON(retried))
			return
		}
	}
	writeHostedCLIError(request.Context(), response, err)
}

// hostedCLIOutput streams NDJSON output events compatible with the local
// dashboard command queue until the command reaches a terminal status.
func (a *App) hostedCLIOutput(response http.ResponseWriter, request *http.Request) {
	if _, ok := a.authorizeHostedCLI(response, request); !ok {
		return
	}
	id, err := commandID(request)
	if err != nil {
		writeHostedCLIError(request.Context(), response, err)
		return
	}
	cursor := int64(0)
	if value := request.URL.Query().Get("after"); value != "" {
		if cursor, err = strconv.ParseInt(value, 10, 64); err != nil || cursor < 0 {
			writeError(request.Context(), response, http.StatusBadRequest, "Invalid output cursor.")
			return
		}
	}
	ctx := request.Context()
	if _, err := a.hostedCLI.queue.Detail(ctx, id); err != nil {
		writeHostedCLIError(request.Context(), response, err)
		return
	}
	controller := http.NewResponseController(response)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(response)
	write := func(event map[string]any) bool {
		// Long-running output must outlive the server-wide write timeout.
		_ = controller.SetWriteDeadline(time.Now().Add(time.Minute))
		if encoder.Encode(event) != nil {
			return false
		}
		return controller.Flush() == nil
	}
	for ctx.Err() == nil {
		chunks, err := a.hostedCLI.queue.Output(ctx, id, cursor, 128)
		if err != nil {
			write(map[string]any{"type": "error", "error": "Command output is unavailable."})
			return
		}
		for _, chunk := range chunks {
			if !write(map[string]any{"type": "output", "sequence": chunk.Sequence, "stream": chunk.Stream, "data": chunk.Data}) {
				return
			}
			cursor = chunk.Sequence
		}
		if len(chunks) == 128 {
			continue
		}
		command, err := a.hostedCLI.queue.Detail(ctx, id)
		if err != nil {
			write(map[string]any{"type": "error", "error": "Command output is unavailable."})
			return
		}
		if command.Status.Terminal() && !command.ExecutionClaimed {
			if remaining, err := a.hostedCLI.queue.Output(ctx, id, cursor, 1); err == nil && len(remaining) > 0 {
				continue
			}
			payload := hostedCLICommandJSON(command)
			write(map[string]any{"type": "complete", "command": payload, "result": payload["result"]})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func hostedCLICommandJSON(command operational.CLICommand) map[string]any {
	terminal := command.Status.Terminal()
	var ok, result any
	if terminal {
		completed := command.Status == operational.CLICommandCompleted
		ok = completed
		value := map[string]any{"ok": completed, "exitCode": command.ExitCode}
		if command.Error != "" {
			value["error"] = command.Error
		}
		result = value
	}
	timestamp := func(value *time.Time) any {
		if value == nil {
			return nil
		}
		return value.UTC().Format(time.RFC3339Nano)
	}
	var retryOf any
	if command.RetryOf > 0 {
		retryOf = command.RetryOf
	}
	return map[string]any{
		"id": command.ID, "actionId": command.ActionID, "command": command.Command,
		"arguments": command.Arguments, "values": command.Values,
		"actor": command.Actor, "repository": command.Repository,
		"status": command.Status, "createdAt": command.CreatedAt.UTC().Format(time.RFC3339Nano),
		"startedAt": timestamp(command.StartedAt), "completedAt": timestamp(command.CompletedAt),
		"deadline": timestamp(command.Deadline), "completionWeek": command.CompletionWeek,
		"idempotencyKey": command.IdempotencyKey, "outputBytes": command.OutputBytes,
		"outputChunks": command.OutputChunks, "outputTruncated": command.OutputTruncated,
		"ok": ok, "exitCode": command.ExitCode, "error": command.Error, "retryOf": retryOf,
		"result": result, "stdout": "", "stderr": "",
	}
}
