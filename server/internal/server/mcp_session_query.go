package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

type mcpArchiveDeliveryKey struct{}

// The HTTP response owns this reader through SDK serialization and final write.
// Neither client metadata nor a tool's ambient context grants trace-read access.
type mcpArchiveDelivery struct {
	request *http.Request
	reader  sessionArchiveReader
	ctx     context.Context
	cancel  context.CancelFunc
}

func (a *App) sessionDataLakeAvailable(request *http.Request) bool {
	return a.sessionArchive != nil &&
		slices.Contains(a.sessionArchive.backend.QueryKinds(), "session-query") &&
		a.sessionArchiveAuthorized(request)
}

//nolint:contextcheck // HTTP owns final delivery; SDK method cancellation is forwarded only during execution.
func (runtime *mcpRuntime) callSessionQuery(ctx context.Context, arguments json.RawMessage) (sessionArchiveResult, error) {
	if err := ctx.Err(); err != nil {
		return sessionArchiveResult{}, err
	}
	delivery, ok := ctx.Value(mcpArchiveDeliveryKey{}).(*mcpArchiveDelivery)
	if !ok || delivery.request == nil || !runtime.app.sessionDataLakeAvailable(delivery.request.WithContext(ctx)) {
		return sessionArchiveResult{}, errors.New("session data lake queries require an enabled archive and separately approved full-trace-read access")
	}
	if len(arguments) == 0 || len(arguments) > 4096 || delivery.reader != nil {
		return sessionArchiveResult{}, errors.New("session data lake query exceeds its request budget")
	}
	var input struct {
		Repository      string                     `json:"repository"`
		LanguageVersion string                     `json:"language-version"`
		Query           *sessionarchive.Definition `json:"query"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
		return sessionArchiveResult{}, errors.New("invalid bounded session data lake query")
	}
	query := sessionArchiveQuery{Kind: "session-query", Repository: input.Repository, LanguageVersion: input.LanguageVersion, Query: input.Query}
	repository, err := collect.NormalizeRepository(input.Repository)
	if err != nil || repository != input.Repository || query.validate() != nil {
		return sessionArchiveResult{}, errors.New("invalid session data lake identity or analytical definition")
	}
	archive := runtime.app.sessionArchive
	if !slices.Contains(archive.repositories, repository) {
		return sessionArchiveResult{}, errors.New("repository is outside the approved session data lake scope")
	}
	plan, err := input.Query.Plan()
	if err != nil || plan.Require(archive.backend.AnalyticalCapabilities()) != nil {
		return sessionArchiveResult{}, errors.New("analytical operators or limit are unavailable on this archive backend")
	}
	// The SDK closes the method context before flushing its response. Keep
	// readers bound to the HTTP request, forwarding method cancellation only
	// while the analytical operation is still executing.
	queryCtx, cancel := context.WithTimeout(delivery.request.Context(), 30*time.Second)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	result, err := archive.backend.Query(queryCtx, query)
	if err != nil || ctx.Err() != nil {
		if result.reader != nil {
			_ = result.reader.Close()
		}
		cancel()
		return sessionArchiveResult{}, errors.New("session data lake query is unavailable or exceeds its approved budget")
	}
	if result.reader == nil {
		cancel()
		return sessionArchiveResult{}, errors.New("session data lake query has no active reader")
	}
	content, err := json.Marshal(result)
	if err != nil || len(content) > 8<<20 || result.Kind != "session-query" || result.Rows == nil ||
		result.Metadata == nil || len(result.Columns) == 0 || len(*result.Rows) > 10000 ||
		len(result.Events) != 0 || len(result.Summary) != 0 {
		_ = result.reader.Close()
		cancel()
		return sessionArchiveResult{}, errors.New("session data lake result exceeds its analytical response contract")
	}
	delivery.ctx, delivery.cancel, delivery.reader = queryCtx, cancel, result.reader
	return result, nil
}

func mcpArchiveErrorBody(body []byte, message string) []byte {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		envelope = map[string]json.RawMessage{"jsonrpc": json.RawMessage(`"2.0"`), "id": json.RawMessage("null")}
	}
	envelope["result"], _ = json.Marshal(mcpErrorResult(message))
	delete(envelope, "error")
	content, _ := json.Marshal(envelope)
	return content
}
