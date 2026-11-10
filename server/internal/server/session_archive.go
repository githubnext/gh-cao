package server

import (
	"bytes"
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
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
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
	Kind            string                     `json:"kind"`
	Repository      string                     `json:"repository"`
	RunID           *string                    `json:"runId,omitempty"`
	Attempt         *int64                     `json:"attempt,omitempty"`
	Limit           *int                       `json:"limit,omitempty"`
	LanguageVersion string                     `json:"language-version,omitempty"`
	Query           *sessionarchive.Definition `json:"query,omitempty"`
}

type sessionArchiveResult struct {
	Generation uint64                        `json:"generation"`
	Kind       string                        `json:"kind"`
	Events     []sessionarchive.Event        `json:"events,omitempty"`
	Summary    []sessionarchive.Summary      `json:"summary,omitempty"`
	Columns    []sessionarchive.Column       `json:"columns,omitempty"`
	Rows       *[]map[string]any             `json:"rows,omitempty"`
	Metadata   *sessionarchive.QueryMetadata `json:"metadata,omitempty"`
	reader     sessionArchiveReader
}

type sessionArchiveReader interface {
	Deliver(func() error) error
	Close() error
}

type sessionArchiveBackend interface {
	collect.TraceCapture
	Flush(context.Context) error
	Maintain(context.Context) error
	Close() error
	Status() sessionarchive.Status
	QueryKinds() []string
	Query(context.Context, sessionArchiveQuery) (sessionArchiveResult, error)
}

func (*localSessionArchive) QueryKinds() []string {
	return []string{"timeline", "summary", "session-query"}
}
func (*hostedSessionArchive) QueryKinds() []string { return []string{"timeline", "summary"} }

type localSessionArchive struct {
	*sessionarchive.Archive
	namespace string
}

type hostedSessionArchive struct {
	*sessionarchive.Archive
	publisher hostedArchivePublisher
	namespace string
}

type hostedArchiveReader interface {
	ReadTimeline(context.Context, sessionarchive.Scope, sessionarchive.TimelineRequest) (uint64, []sessionarchive.Event, *sessionarchiveblob.Reader, error)
	ReadSummaries(context.Context, sessionarchive.Scope, string, int) (uint64, []sessionarchive.Summary, *sessionarchiveblob.Reader, error)
}

type hostedArchivePublisher interface {
	sessionarchive.Publisher
	hostedArchiveReader
	WithdrawRepository(context.Context, string) error
	Maintain(context.Context) error
	Close() error
}

func (archive *hostedSessionArchive) Query(ctx context.Context, input sessionArchiveQuery) (sessionArchiveResult, error) {
	return hostedArchiveQuery(ctx, archive.publisher, archive.namespace, input)
}

type nativeSessionArchive struct {
	*sessionarchiveblob.Native
	namespace   string
	destination string
	authority   sessionarchive.NativeAuthority
}

func (*nativeSessionArchive) QueryKinds() []string { return []string{"timeline", "summary"} }

func (archive *nativeSessionArchive) Query(ctx context.Context, input sessionArchiveQuery) (sessionArchiveResult, error) {
	if archive.authority == nil {
		return sessionArchiveResult{}, collectnative.ErrUnsupported
	}
	if err := input.validate(); err != nil {
		return sessionArchiveResult{}, err
	}
	source, err := archive.authority.Current(ctx, archive.destination, input.Repository)
	if err != nil {
		return sessionArchiveResult{}, err
	}
	if source.Validate() != nil || source.Repository != input.Repository {
		return sessionArchiveResult{}, sessionarchive.ErrWithdrawn
	}
	scope := sessionarchive.Scope{Namespace: archive.namespace, Repositories: []string{input.Repository}}
	result := sessionArchiveResult{Kind: input.Kind}
	var reader *sessionarchiveblob.Reader
	switch input.Kind {
	case "timeline":
		result.Generation, result.Events, reader, err = archive.ReadTimelineAt(ctx, scope, source,
			sessionarchive.TimelineRequest{Repository: input.Repository, RunID: *input.RunID, Attempt: *input.Attempt, Limit: min(input.rowLimit(), 4096)})
	case "summary":
		result.Generation, result.Summary, reader, err = archive.ReadSummariesAt(ctx, scope, source, input.Repository, min(input.rowLimit(), 4096))
	default:
		return result, sessionarchive.ErrQuery
	}
	if reader != nil {
		result.reader = &authorizedArchiveReader{inner: reader, authority: archive.authority, destination: archive.destination, source: source, ctx: ctx}
	}
	return result, err
}

type authorizedArchiveReader struct {
	inner       sessionArchiveReader
	authority   sessionarchive.NativeAuthority
	destination string
	source      sessionarchive.Source
	ctx         context.Context
}

func (r *authorizedArchiveReader) Close() error { return r.inner.Close() }

func (r *authorizedArchiveReader) Deliver(deliver func() error) error {
	return r.authority.WithRead(r.ctx, r.destination, r.source, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return r.inner.Deliver(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return deliver()
		})
	})
}

func (archive *nativeSessionArchive) Flush(ctx context.Context) error {
	if archive.authority == nil {
		return collectnative.ErrUnsupported
	}
	if _, err := archive.authority.DrainWithdrawals(ctx, archive.destination, 64, archive.WithdrawSource); err != nil {
		return err
	}
	return archive.Native.Flush(ctx)
}

func (*nativeSessionArchive) WithdrawRepository(context.Context, string) error {
	// Bare repository callbacks cannot distinguish a withdrawn source epoch
	// from a reenrollment. Enrollment must commit the durable PG outbox instead.
	return collectnative.ErrUnsupported
}

func (*nativeSessionArchive) PrepareCache(context.Context, string, string, string) error {
	return collectnative.ErrUnsupported
}

func (*nativeSessionArchive) Capture(context.Context, string, string, string) error {
	return collectnative.ErrUnsupported
}

func hostedArchiveQuery(ctx context.Context, publisher hostedArchiveReader, namespace string, input sessionArchiveQuery) (sessionArchiveResult, error) {
	if err := input.validate(); err != nil {
		return sessionArchiveResult{}, err
	}
	scope := sessionarchive.Scope{Namespace: namespace, Repositories: []string{input.Repository}}
	result := sessionArchiveResult{Kind: input.Kind}
	var reader *sessionarchiveblob.Reader
	var err error
	switch input.Kind {
	case "timeline":
		result.Generation, result.Events, reader, err = publisher.ReadTimeline(ctx, scope, sessionarchive.TimelineRequest{
			Repository: input.Repository, RunID: *input.RunID, Attempt: *input.Attempt, Limit: min(input.rowLimit(), 4096),
		})
	case "summary":
		result.Generation, result.Summary, reader, err = publisher.ReadSummaries(ctx, scope, input.Repository, min(input.rowLimit(), 4096))
	default:
		return sessionArchiveResult{}, sessionarchive.ErrQuery
	}
	if reader != nil {
		result.reader = reader
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
	if err := input.validate(); err != nil {
		return sessionArchiveResult{}, err
	}
	snapshot, err := archive.Pin(ctx, sessionarchive.Scope{
		Namespace: archive.namespace, Repositories: []string{input.Repository},
	})
	if err != nil {
		return sessionArchiveResult{}, err
	}
	result := sessionArchiveResult{Kind: input.Kind, Generation: snapshot.Generation(), reader: snapshot}
	switch input.Kind {
	case "timeline":
		result.Events, err = snapshot.Timeline(ctx, sessionarchive.TimelineRequest{
			Repository: input.Repository, RunID: *input.RunID, Attempt: *input.Attempt, Limit: input.rowLimit(),
		})
	case "summary":
		result.Summary, err = snapshot.Summary(ctx)
	case "session-query":
		var analytical sessionarchive.AnalyticalResult
		analytical, err = snapshot.Analytical(ctx, *input.Query)
		result.Columns, result.Rows, result.Metadata = analytical.Columns, &analytical.Rows, &analytical.Metadata
	default:
		err = sessionarchive.ErrQuery
	}
	if err != nil {
		_ = snapshot.Close()
		result.reader = nil
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

// CaptureSession cannot infer a dispatch-time epoch from current enrollment.
// Native callers must obtain NativeDestination with their original scope.
func (archive *sessionArchiveService) CaptureSession(ctx context.Context, repository, runID string, attempt int64, completed time.Time, content []byte, gap string) error {
	return collectnative.ErrUnsupported
}

// NativeDestination binds the dispatch-time source, never the source that
// happens to be enrolled when an old queue item is executed.
func (archive *sessionArchiveService) NativeDestination(scope collectnative.EnrollmentScope) (collectnative.Destination, error) {
	native, ok := archive.backend.(*nativeSessionArchive)
	source := sessionarchive.Source{Repository: scope.Repository, InstallationID: scope.InstallationID, Epoch: scope.Epoch}
	if !ok || native.authority == nil {
		return nil, collectnative.ErrUnsupported
	}
	if err := source.Validate(); err != nil {
		return nil, err
	}
	return &nativeArchiveDestination{archive: native, source: source, enabled: slices.Contains(archive.repositories, source.Repository)}, nil
}

type nativeArchiveDestination struct {
	archive *nativeSessionArchive
	source  sessionarchive.Source
	enabled bool
}

func (d *nativeArchiveDestination) Capture(ctx context.Context, run collectnative.Run, content []byte, gap string) error {
	if run.Repository != d.source.Repository || run.InstallationID != d.source.InstallationID {
		return collectnative.ErrIdentity
	}
	return (collectnative.TraceDestination{Admission: d}).Capture(ctx, run, content, gap)
}

func (d *nativeArchiveDestination) CaptureSession(ctx context.Context, repository, runID string, attempt int64, completed time.Time, content []byte, gap string) error {
	if repository != d.source.Repository {
		return collectnative.ErrIdentity
	}
	if !d.enabled {
		return nil
	}
	return d.archive.admit(ctx, d.source, func(ctx context.Context) error {
		return d.archive.CaptureSessionAt(ctx, d.source, runID, attempt, completed, content, gap)
	})
}

func (a *nativeSessionArchive) admit(ctx context.Context, source sessionarchive.Source, capture func(context.Context) error) error {
	if a.authority == nil {
		return collectnative.ErrUnsupported
	}
	if err := a.authority.Register(ctx, a.destination, source); err != nil {
		return err
	}
	if err := capture(ctx); err != nil {
		return err
	}
	current, err := a.authority.Current(ctx, a.destination, source.Repository)
	if err != nil {
		return err
	}
	if current != source {
		return sessionarchive.ErrWithdrawn
	}
	return nil
}

//nolint:contextcheck // Close is the context-free owned Blob lifecycle cleanup.
func openNativeSessionArchive(ctx context.Context, config sessionarchive.Config, blob sessionarchiveblob.Config, authority sessionarchive.NativeAuthority) (*nativeSessionArchive, error) {
	if authority == nil {
		return nil, errors.Join(collectnative.ErrUnsupported,
			errors.New("native archive requires PG enrollment-atomic source fencing and a durable withdrawal outbox"))
	}
	archive, err := sessionarchiveblob.NewNative(ctx, config, blob)
	if err != nil {
		return nil, err
	}
	if err := archive.RequireSourceFencing(ctx); err != nil {
		return nil, errors.Join(err, archive.Close())
	}
	return &nativeSessionArchive{Native: archive, namespace: config.Namespace,
		destination: blob.NativeAcceptanceBinding(), authority: authority}, nil
}

func (a *App) nativeSessionArchiveAuthority(ctx context.Context) (sessionarchive.NativeAuthority, error) {
	if a.config.SessionArchiveAuthority != nil {
		return a.config.SessionArchiveAuthority, nil
	}
	database, ok := a.database.(*postgresx.Store)
	if !ok || !database.Open() {
		return nil, errors.Join(collectnative.ErrUnsupported,
			errors.New("native session archive requires available canonical PostgreSQL"))
	}
	if _, err := database.State(ctx); err != nil {
		return nil, errors.Join(errors.New("native session archive canonical PostgreSQL is unavailable"), err)
	}
	return database.ArchiveAuthority(), nil
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
		if config.NativeProducer {
			return errors.New("native session archive requires the hosted Blob profile")
		}
		if blob.Enabled {
			return errors.New("local SQLite session archive cannot select hosted Blob/ADX analytics")
		}
		repositories, err := database.SnapshotRepositories(ctx)
		if err != nil {
			return err
		}
		for _, repository := range config.Repositories {
			if !slices.Contains(repositories, repository) {
				return errors.New("session archive scope must be contained in the local snapshot inventory")
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
	if a.config.Collector != nil && a.config.Collector.NativeAcquisition && !config.NativeProducer {
		return errors.New("native collection requires the native session archive producer; legacy filesystem capture is unsupported")
	}
	switch {
	case config.NativeProducer:
		if a.config.Collector == nil || !a.config.Collector.NativeAcquisition {
			return errors.New("native session archive requires explicitly selected native acquisition")
		}
		authority, err := a.nativeSessionArchiveAuthority(ctx)
		if err != nil {
			return err
		}
		archive, err := openNativeSessionArchive(ctx, config, blob, authority)
		if err != nil {
			return err
		}
		backend = archive
	case blob.Enabled:
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
	default:
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
		if collector.native == nil {
			return errors.New("native session archive requires assembled native collection")
		}
		collector.native.archive = a.sessionArchive
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
		writeError(request.Context(), response, http.StatusForbidden, "separately approved full-trace-read access is required")
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
		"queryKinds": a.sessionArchive.backend.QueryKinds(),
	})
}

func (a *App) sessionArchiveQuery(response http.ResponseWriter, request *http.Request) {
	if a.sessionArchive == nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "session archive is disabled")
		return
	}
	if !a.sessionArchiveAuthorized(request) {
		writeError(request.Context(), response, http.StatusForbidden, "separately approved full-trace-read access is required")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input sessionArchiveQuery
	if err := decoder.Decode(&input); err != nil {
		writeError(request.Context(), response, http.StatusBadRequest, "invalid bounded archive query")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(request.Context(), response, http.StatusBadRequest, "archive query must contain exactly one JSON object")
		return
	}
	repository, err := collect.NormalizeRepository(input.Repository)
	if err != nil || repository != input.Repository {
		writeError(request.Context(), response, http.StatusBadRequest, "archive queries require an exact normalized repository")
		return
	}
	if !slices.Contains(a.sessionArchive.repositories, repository) {
		writeError(request.Context(), response, http.StatusForbidden, "repository is outside the approved archive scope")
		return
	}
	if err := input.validate(); err != nil {
		writeError(request.Context(), response, http.StatusBadRequest, "archive query requires a trusted template and bounded exact identity")
		return
	}
	if !slices.Contains(a.sessionArchive.backend.QueryKinds(), input.Kind) {
		writeError(request.Context(), response, http.StatusUnprocessableEntity, "analytical query profile is unavailable on this archive backend")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	result, err := a.sessionArchive.backend.Query(ctx, input)
	if result.reader != nil {
		defer func() { _ = result.reader.Close() }()
	}
	writeQueryError := func(err error) {
		serverLog.PrintfContext(ctx, "session archive query failed")
		status := http.StatusServiceUnavailable
		switch {
		case errors.Is(err, sessionarchive.ErrQuery):
			status = http.StatusBadRequest
		case errors.Is(err, sessionarchive.ErrCapacity), errors.Is(err, sessionarchiveblob.ErrCapacity), errors.Is(err, sessionarchive.ErrLimit):
			status = http.StatusUnprocessableEntity
		case errors.Is(err, sessionarchive.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, sessionarchive.ErrWithdrawn), errors.Is(err, sessionarchiveblob.ErrWithdrawn), errors.Is(err, sessionarchiveblob.ErrScope):
			status = http.StatusForbidden
		}
		writeError(ctx, response, status, "session archive query is unavailable or exceeds its approved budget")
	}
	if err != nil {
		writeQueryError(err)
		return
	}
	if result.reader == nil {
		writeQueryError(errors.New("session archive result has no active reader"))
		return
	}
	content, err := json.Marshal(result)
	if err != nil || len(content) > 8<<20 || len(result.Summary) > input.rowLimit() || len(result.Events) > input.rowLimit() ||
		(result.Rows != nil && len(*result.Rows) > 10000) {
		writeError(ctx, response, http.StatusUnprocessableEntity, "complete archive result exceeds the response budget")
		return
	}
	deadline, _ := ctx.Deadline()
	if err := http.NewResponseController(response).SetWriteDeadline(deadline); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		writeQueryError(err)
		return
	}
	started := false
	err = result.reader.Deliver(func() error {
		if !a.sessionArchiveAuthorized(request) {
			writeError(ctx, response, http.StatusForbidden, "full-trace-read authorization is no longer available")
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		started = true
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, err := response.Write(content)
		return err
	})
	if err != nil {
		if !started {
			writeQueryError(err)
		} else {
			serverLog.PrintfContext(ctx, "session archive response delivery failed")
		}
	}
}

// Pointer fields track omitted selectors, but JSON null is not an omission.
func (input *sessionArchiveQuery) UnmarshalJSON(content []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil {
		return err
	}
	for _, value := range fields {
		if bytes.Equal(value, []byte("null")) {
			return sessionarchive.ErrQuery
		}
	}
	type archiveQuery sessionArchiveQuery
	var decoded archiveQuery
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*input = sessionArchiveQuery(decoded)
	return nil
}

func (input sessionArchiveQuery) rowLimit() int {
	if input.Limit != nil {
		return *input.Limit
	}
	return 10000
}

func (input sessionArchiveQuery) validate() error {
	if input.rowLimit() < 1 || input.rowLimit() > 10000 {
		return sessionarchive.ErrQuery
	}
	if input.Kind == "session-query" {
		if input.LanguageVersion != "0.1.0" || input.Query == nil || input.RunID != nil || input.Attempt != nil || input.Limit != nil {
			return sessionarchive.ErrQuery
		}
		return input.Query.Validate()
	}
	if input.Query != nil || input.LanguageVersion != "" {
		return sessionarchive.ErrQuery
	}
	switch input.Kind {
	case "summary":
		if input.RunID != nil || input.Attempt != nil {
			return sessionarchive.ErrQuery
		}
	case "timeline":
		if input.RunID == nil || input.Attempt == nil || *input.Attempt < 1 {
			return sessionarchive.ErrQuery
		}
		run, err := strconv.ParseUint(*input.RunID, 10, 64)
		if err != nil || run == 0 || strconv.FormatUint(run, 10) != *input.RunID {
			return sessionarchive.ErrQuery
		}
	default:
		return sessionarchive.ErrQuery
	}
	return nil
}
