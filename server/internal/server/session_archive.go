package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
	"github.com/githubnext/gh-cao/server/internal/sessionarchiveblob"
	"github.com/githubnext/gh-cao/server/internal/sqlitex"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var archiveSummary = logger.NewSlogLoggerWithHandler(logger.New("cao:session-archive"))

// SessionArchiveReadAccess is an additional content grant, not ordinary
// dashboard membership or administrator permission. Users grants the exact
// configured archive scope to the named hosted logins.
type SessionArchiveReadAccess struct {
	Approved bool
	Users    []string
}

type sessionArchiveQuery struct {
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	RunID      string `json:"runId,omitempty"`
	Attempt    int64  `json:"attempt,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type sessionArchiveResult struct {
	Generation uint64                   `json:"generation"`
	Kind       string                   `json:"kind"`
	Events     []sessionarchive.Event   `json:"events,omitempty"`
	Summary    []sessionarchive.Summary `json:"summary,omitempty"`
}

type sessionArchiveBackend interface {
	collect.TraceCapture
	Flush(context.Context) error
	Maintain(context.Context) error
	Close() error
	Status() sessionarchive.Status
	Query(context.Context, sessionArchiveQuery) (sessionArchiveResult, error)
}

type localSessionArchive struct {
	*sessionarchive.Archive
	namespace string
}

type hostedSessionArchive struct {
	*sessionarchive.Archive
	publisher *sessionarchiveblob.Archive
	namespace string
}

func (archive *hostedSessionArchive) Query(ctx context.Context, input sessionArchiveQuery) (sessionArchiveResult, error) {
	scope := sessionarchive.Scope{Namespace: archive.namespace, Repositories: []string{input.Repository}}
	result := sessionArchiveResult{Kind: input.Kind}
	var err error
	switch input.Kind {
	case "timeline":
		result.Generation, result.Events, err = archive.publisher.Timeline(ctx, scope, sessionarchive.TimelineRequest{
			Repository: input.Repository, RunID: input.RunID, Attempt: input.Attempt, Limit: min(input.Limit, 4096),
		})
	case "summary":
		result.Generation, result.Summary, err = archive.publisher.Summaries(ctx, scope, input.Repository, min(input.Limit, 4096))
	}
	return result, err
}

func (archive *hostedSessionArchive) WithdrawRepository(ctx context.Context, repository string) error {
	remote := archive.publisher.WithdrawRepository(ctx, repository)
	return errors.Join(remote, archive.Archive.WithdrawRepository(ctx, repository))
}

func (archive *hostedSessionArchive) Maintain(ctx context.Context) error {
	return errors.Join(archive.Archive.Maintain(ctx), archive.publisher.Maintain(ctx))
}

func (archive *hostedSessionArchive) Close() error {
	return errors.Join(archive.Archive.Close(), archive.publisher.Close())
}

func (archive *localSessionArchive) Query(ctx context.Context, input sessionArchiveQuery) (sessionArchiveResult, error) {
	snapshot, err := archive.Pin(ctx, sessionarchive.Scope{
		Namespace: archive.namespace, Repositories: []string{input.Repository},
	})
	if err != nil {
		return sessionArchiveResult{}, err
	}
	defer func() { _ = snapshot.Close() }()
	result := sessionArchiveResult{Kind: input.Kind, Generation: snapshot.Generation()}
	switch input.Kind {
	case "timeline":
		result.Events, err = snapshot.Timeline(ctx, sessionarchive.TimelineRequest{
			Repository: input.Repository, RunID: input.RunID, Attempt: input.Attempt, Limit: input.Limit,
		})
	case "summary":
		result.Summary, err = snapshot.Summary(ctx)
	}
	return result, err
}

type sessionArchiveService struct {
	backend      sessionArchiveBackend
	repositories []string
	read         SessionArchiveReadAccess
	mu           sync.Mutex
	retrying     bool
}

func (archive *sessionArchiveService) PrepareCache(ctx context.Context, repository, source, target string) error {
	return archive.backend.PrepareCache(ctx, repository, source, target)
}

func (archive *sessionArchiveService) Capture(ctx context.Context, repository, logs, prefix string) error {
	return archive.backend.Capture(ctx, repository, logs, prefix)
}

func (archive *sessionArchiveService) WithdrawRepository(ctx context.Context, repository string) error {
	if !slices.Contains(archive.repositories, repository) {
		return nil
	}
	return archive.backend.WithdrawRepository(ctx, repository)
}

func resolveSessionArchiveConfig(config Config) (sessionarchive.Config, SessionArchiveReadAccess, error) {
	var archive sessionarchive.Config
	var err error
	if config.SessionArchive == nil {
		archive, err = sessionarchive.FromEnv(os.Getenv)
	} else {
		archive = *config.SessionArchive
		err = archive.Validate()
	}
	if err != nil || !archive.Enabled {
		return archive, SessionArchiveReadAccess{}, err
	}
	if config.Canvas != nil || config.HostProfile.Listener == HostListenerPlatform {
		return archive, SessionArchiveReadAccess{}, errors.New("session archive requires a persistent non-canvas server lifecycle")
	}
	if config.Collector != nil && (config.Collector.AdmitOnly || config.Collector.Workers <= 0) {
		return archive, SessionArchiveReadAccess{}, errors.New("session archive requires co-resident collection workers")
	}
	if _, local := config.Database.(*sqlitex.Store); !local && config.Collector == nil {
		return archive, SessionArchiveReadAccess{}, errors.New("session archive requires local SQLite or co-resident collection")
	}
	read := SessionArchiveReadAccess{}
	if config.SessionArchiveRead != nil {
		read = *config.SessionArchiveRead
		read.Users = slices.Clone(read.Users)
	} else {
		switch os.Getenv("CAO_SESSION_ARCHIVE_READ_APPROVED") {
		case "", "false":
		case "true":
			read.Approved = true
		default:
			return archive, read, errors.New("CAO_SESSION_ARCHIVE_READ_APPROVED must be true or false")
		}
		if users := os.Getenv("CAO_SESSION_ARCHIVE_READ_USERS"); users != "" {
			read.Users = strings.Split(users, ",")
		}
	}
	for i, user := range read.Users {
		user = strings.ToLower(strings.TrimSpace(user))
		if user == "" || len(user) > 39 || strings.IndexFunc(user, func(c rune) bool {
			return c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9')
		}) >= 0 {
			return archive, read, errors.New("session archive read users must be exact GitHub logins")
		}
		read.Users[i] = user
	}
	if read.Approved && config.GitHubOAuth != nil && len(read.Users) == 0 {
		return archive, read, errors.New("hosted session archive reads require an explicit full-trace-read user allowlist")
	}
	return archive, read, nil
}

//nolint:contextcheck // The bounded archive constructors and Close expose context-free lifecycle APIs.
func (a *App) configureSessionArchive(ctx context.Context) error {
	config, read, err := resolveSessionArchiveConfig(a.config)
	if err != nil {
		return err
	}
	var blob sessionarchiveblob.Config
	if a.config.SessionArchiveBlob == nil {
		blob, err = sessionarchiveblob.FromEnv(os.Getenv)
	} else {
		blob = *a.config.SessionArchiveBlob
	}
	if err != nil {
		return err
	}
	if !config.Enabled {
		if blob.Enabled {
			return errors.New("hosted Blob archive requires independently enabled approved archive capture")
		}
		return nil
	}
	if database, local := a.database.(*sqlitex.Store); local {
		if blob.Enabled {
			return errors.New("local SQLite session archive cannot select hosted Blob/ADX analytics")
		}
		targets, err := database.BackfillTargets(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		for _, repository := range config.Repositories {
			if !slices.ContainsFunc(targets, func(target sqlitex.BackfillTarget) bool {
				return target.Repository == repository
			}) {
				return errors.New("session archive scope must be contained in the local snapshot collection scope")
			}
		}
	} else {
		if !a.config.SingleReplicaConfirmed || a.oauth == nil || !blob.Enabled {
			return errors.New("hosted session archive requires Blob/ADX, OAuth and a confirmed single persistent replica")
		}
		if blob.Namespace != config.Namespace || !slices.Equal(blob.RepositoryIDs, config.Repositories) {
			return errors.New("hosted archive destination scope must exactly match approved capture scope")
		}
	}
	var backend sessionArchiveBackend
	if blob.Enabled {
		converter, err := sessionarchive.NewConverter(sessionarchive.ConverterConfig{
			DuckDBPath: config.DuckDBPath, Timeout: config.QueryTimeout,
		})
		if err != nil {
			return err
		}
		publisher, err := sessionarchiveblob.New(ctx, blob, converter)
		if err != nil {
			return err
		}
		archive, err := sessionarchive.OpenWithPublisher(config, publisher)
		if err != nil {
			return errors.Join(err, publisher.Close())
		}
		backend = &hostedSessionArchive{Archive: archive, publisher: publisher, namespace: config.Namespace}
	} else {
		archive, err := sessionarchive.Open(config)
		if err != nil {
			return err
		}
		backend = &localSessionArchive{Archive: archive, namespace: config.Namespace}
	}
	a.sessionArchive = &sessionArchiveService{
		backend:      backend,
		repositories: slices.Clone(config.Repositories), read: read,
	}
	if collector := a.Collector(); collector != nil {
		if collector.runner.Traces == nil {
			collector.runner.Traces = a.sessionArchive
		} else {
			collector.runner.Traces = collect.TraceCaptures{collector.runner.Traces, a.sessionArchive}
		}
	}
	return nil
}

func (archive *sessionArchiveService) run(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		err := archive.publish(ctx)
		archive.mu.Lock()
		archive.retrying = err != nil
		archive.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			delay = min(time.Minute, delay*2)
		} else {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (archive *sessionArchiveService) publish(ctx context.Context) error {
	before := archive.backend.Status()
	started := time.Now()
	ctx, span := telemetry.Tracer().Start(ctx, "cao_dashboard.session_archive.publish")
	defer span.End()
	err := archive.backend.Flush(ctx)
	if err == nil {
		err = archive.backend.Maintain(ctx)
	}
	status := archive.backend.Status()
	span.SetAttributes(attribute.Int("cao_dashboard.archive.active_traces", status.ActiveTraces),
		attribute.Int("cao_dashboard.archive.staged_traces", status.StagedTraces),
		attribute.Int("cao_dashboard.archive.gaps", status.Gaps),
		attribute.Int64("cao_dashboard.archive.archive_bytes", status.ArchiveBytes),
		attribute.Int64("cao_dashboard.archive.staging_bytes", status.StagingBytes))
	if err != nil {
		span.SetStatus(codes.Error, "archive publication incomplete")
	} else {
		span.SetStatus(codes.Ok, "")
	}
	if err != nil || status.Generation != before.Generation || status.StagedTraces != before.StagedTraces || status.Gaps != before.Gaps {
		archiveSummary.InfoContext(ctx, "archive publication",
			slog.Bool("retrying", err != nil), slog.Uint64("generation", status.Generation),
			slog.Int("active_traces", status.ActiveTraces), slog.Int("staged_traces", status.StagedTraces),
			slog.Int("gaps", status.Gaps),
			slog.Int64("archive_bytes", status.ArchiveBytes), slog.Int64("staging_bytes", status.StagingBytes),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()))
	}
	return err
}

func (a *App) sessionArchiveAuthorized(request *http.Request) bool {
	archive := a.sessionArchive
	if archive == nil || !archive.read.Approved {
		return false
	}
	if a.oauth == nil {
		return a.authorized(request)
	}
	// Reload after execution as well: a revoked session cannot receive a
	// completed result merely because it was valid when the query started.
	session, ok := a.oauth.loadRequestSession(request)
	return ok && a.oauth.adminAssured(session, time.Now().UTC()) &&
		slices.Contains(archive.read.Users, strings.ToLower(session.Login))
}

func (a *App) sessionArchiveStatus(response http.ResponseWriter, request *http.Request) {
	if a.sessionArchive == nil {
		writeJSON(response, http.StatusOK, map[string]any{"enabled": false, "stage": "disabled"})
		return
	}
	if !a.sessionArchiveAuthorized(request) {
		writeError(response, http.StatusForbidden, "separately approved full-trace-read access is required")
		return
	}
	a.sessionArchive.mu.Lock()
	retrying := a.sessionArchive.retrying
	a.sessionArchive.mu.Unlock()
	stage := "active"
	if retrying {
		stage = "retrying"
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"enabled": true, "stage": stage, "archive": a.sessionArchive.backend.Status(),
	})
}

func (a *App) sessionArchiveQuery(response http.ResponseWriter, request *http.Request) {
	if a.sessionArchive == nil {
		writeError(response, http.StatusServiceUnavailable, "session archive is disabled")
		return
	}
	if !a.sessionArchiveAuthorized(request) {
		writeError(response, http.StatusForbidden, "separately approved full-trace-read access is required")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input sessionArchiveQuery
	if err := decoder.Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid bounded archive query")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(response, http.StatusBadRequest, "archive query must contain exactly one JSON object")
		return
	}
	repository, err := collect.NormalizeRepository(input.Repository)
	if err != nil || repository != input.Repository {
		writeError(response, http.StatusBadRequest, "archive queries require an exact normalized repository")
		return
	}
	if !slices.Contains(a.sessionArchive.repositories, repository) {
		writeError(response, http.StatusForbidden, "repository is outside the approved archive scope")
		return
	}
	if input.Limit == 0 {
		input.Limit = 10000
	}
	run, runErr := strconv.ParseUint(input.RunID, 10, 64)
	if input.Limit < 1 || input.Limit > 10000 ||
		(input.Kind != "timeline" && input.Kind != "summary") ||
		(input.Kind == "timeline" && (runErr != nil || run == 0 || strconv.FormatUint(run, 10) != input.RunID || input.Attempt < 1)) ||
		(input.Kind == "summary" && (input.RunID != "" || input.Attempt != 0)) {
		writeError(response, http.StatusBadRequest, "archive query requires a trusted template and bounded exact identity")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	result, err := a.sessionArchive.backend.Query(ctx, input)
	if err != nil {
		serverLog.Printf("session archive query failed")
		status := http.StatusServiceUnavailable
		switch {
		case errors.Is(err, sessionarchive.ErrCapacity), errors.Is(err, sessionarchiveblob.ErrCapacity), errors.Is(err, sessionarchive.ErrLimit):
			status = http.StatusUnprocessableEntity
		case errors.Is(err, sessionarchive.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, sessionarchive.ErrWithdrawn), errors.Is(err, sessionarchiveblob.ErrWithdrawn), errors.Is(err, sessionarchiveblob.ErrScope):
			status = http.StatusForbidden
		}
		writeError(response, status, "session archive query is unavailable or exceeds its approved budget")
		return
	}
	content, err := json.Marshal(result)
	if err != nil || len(content) > 8<<20 || len(result.Summary) > input.Limit || len(result.Events) > input.Limit {
		writeError(response, http.StatusUnprocessableEntity, "complete archive result exceeds the response budget")
		return
	}
	if !a.sessionArchiveAuthorized(request) {
		writeError(response, http.StatusForbidden, "full-trace-read authorization is no longer available")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content)
}
