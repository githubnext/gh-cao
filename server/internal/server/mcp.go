package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/codes"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var mcpLog = logger.New("cao:server:mcp")

type mcpContract struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Instructions string `json:"instructions"`
	Limits       struct {
		MaxRequestBytes    int64 `json:"maxRequestBytes"`
		DefaultQueryRows   int   `json:"defaultQueryRows"`
		MaxQueryRows       int   `json:"maxQueryRows"`
		MaxParameters      int   `json:"maxParameters"`
		MaxParameterLength int   `json:"maxParameterLength"`
	} `json:"limits"`
	Tools       []mcpContractTool `json:"tools"`
	ServerTools []mcpContractTool `json:"serverTools"`
}

type mcpContractTool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
	Annotations  map[string]any `json:"annotations"`
}

type agentCatalog struct {
	Dashboard map[string]any `json:"dashboard"`
	Pages     []agentPage    `json:"pages"`
	Queries   []agentQuery   `json:"queries"`
}

type agentPage struct {
	ID string `json:"id"`
}

type agentQuery struct {
	ID         string           `json:"id"`
	Parameters []queryParameter `json:"parameters"`
	Execution  struct {
		Local        bool     `json:"local"`
		Requirements []string `json:"requirements"`
		Reason       string   `json:"reason"`
	} `json:"execution"`
}

type queryParameter struct {
	Name     string `json:"name"`
	Field    string `json:"field,omitempty"`
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required,omitempty"`
	Schema   struct {
		Minimum    *float64 `json:"minimum,omitempty"`
		Maximum    *float64 `json:"maximum,omitempty"`
		MultipleOf *float64 `json:"multipleOf,omitempty"`
		Enum       []any    `json:"enum,omitempty"`
		Default    any      `json:"default,omitempty"`
	} `json:"schema,omitempty"`
}

type mcpRuntime struct {
	app        *App
	contract   mcpContract
	catalog    agentCatalog
	catalogRaw map[string]any
}

func (a *App) newMCPHandler() (http.Handler, error) {
	contract, err := readJSONFile[mcpContract](a.config.MCPContractPath)
	if err != nil {
		return nil, fmt.Errorf("read contract: %w", err)
	}
	if contract.ProtocolVersion == "" || len(contract.Tools) == 0 {
		return nil, errors.New("contract must declare a protocol version and tools")
	}
	if contract.Limits.MaxRequestBytes <= 0 || contract.Limits.DefaultQueryRows <= 0 ||
		contract.Limits.MaxQueryRows < contract.Limits.DefaultQueryRows ||
		contract.Limits.MaxParameters <= 0 || contract.Limits.MaxParameterLength <= 0 {
		return nil, errors.New("contract declares invalid resource limits")
	}
	catalog, err := readJSONFile[agentCatalog](a.config.AgentCatalogPath)
	if err != nil {
		return nil, fmt.Errorf("read agent catalog: %w", err)
	}
	for _, entry := range catalog.Queries {
		if _, ok := findDefinition(a.config.DashboardQueries, entry.ID); !ok {
			return nil, fmt.Errorf("agent catalog query %q has no loaded dashboard definition", entry.ID)
		}
	}
	catalogRaw, err := readJSONFile[map[string]any](a.config.AgentCatalogPath)
	if err != nil {
		return nil, fmt.Errorf("read agent catalog payload: %w", err)
	}
	runtime := &mcpRuntime{app: a, contract: contract, catalog: catalog, catalogRaw: catalogRaw}
	server := mcp.NewServer(&mcp.Implementation{
		Name: contract.ServerInfo.Name, Title: contract.ServerInfo.Title, Version: contract.ServerInfo.Version,
	}, &mcp.ServerOptions{
		Instructions:              contract.Instructions,
		SupportedProtocolVersions: []string{contract.ProtocolVersion},
	})
	server.AddReceivingMiddleware(mcpServerTelemetry())
	tools := slices.Clone(contract.Tools)
	for _, tool := range tools {
		if tool.Name != "cao_catalog" && tool.Name != "cao_query" {
			return nil, fmt.Errorf("contract declares unsupported tool %q", tool.Name)
		}
	}
	for index, tool := range contract.ServerTools {
		if tool.Name != "cao_logs" {
			return nil, fmt.Errorf("contract declares unsupported server tool %q", tool.Name)
		}
		if index > 0 {
			return nil, fmt.Errorf("contract declares tool %q more than once", tool.Name)
		}
		if a.logs != nil {
			tools = append(tools, tool)
		}
	}
	registered := map[string]bool{}
	for _, declared := range tools {
		tool := declared
		if registered[tool.Name] {
			return nil, fmt.Errorf("contract declares tool %q more than once", tool.Name)
		}
		registered[tool.Name] = true
		server.AddTool(&mcp.Tool{
			Name: tool.Name, Title: tool.Title, Description: tool.Description,
			InputSchema: tool.InputSchema, OutputSchema: tool.OutputSchema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return runtime.call(ctx, tool.Name, request.Params.Arguments), nil
		})
	}
	if !registered["cao_catalog"] || !registered["cao_query"] {
		return nil, errors.New("contract must declare cao_catalog and cao_query")
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: contract.Limits.MaxRequestBytes,
		PropagateRequestCancellation: true,
	})
	mcpLog.Printf("mcp handler constructed tools=%d default_rows=%d max_rows=%d",
		len(tools), contract.Limits.DefaultQueryRows, contract.Limits.MaxQueryRows)
	return &mcpContractHandler{next: handler, tools: tools}, nil
}

func readJSONFile[T any](path string) (T, error) {
	var value T
	if strings.TrimSpace(path) == "" {
		return value, errors.New("path is required")
	}
	// #nosec G304 -- these paths are explicit operator configuration.
	data, err := dashboardassets.ReadFile(path)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (runtime *mcpRuntime) call(ctx context.Context, name string, arguments json.RawMessage) *mcp.CallToolResult {
	var args map[string]any
	if len(arguments) == 0 {
		args = map[string]any{}
	} else if err := json.Unmarshal(arguments, &args); err != nil {
		return mcpErrorResult("Tool arguments must be an object")
	}
	var (
		payload any
		err     error
	)
	switch name {
	case "cao_catalog":
		payload, err = runtime.callCatalog(args)
	case "cao_query":
		payload, err = runtime.callQuery(ctx, args)
	case "cao_logs":
		payload, err = runtime.callLogs(ctx, args)
	default:
		err = fmt.Errorf("Unknown tool: %s", name) //nolint:staticcheck // Shared MCP error contract uses sentence capitalization.
	}
	if err != nil {
		return mcpErrorResult(err.Error())
	}
	data, _ := json.Marshal(payload)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
		StructuredContent: payload,
	}
}

func (runtime *mcpRuntime) callLogs(ctx context.Context, args map[string]any) (any, error) {
	if !runtime.app.serverLogsAuthorized(ctx) {
		return nil, errors.New("administrator access is required")
	}
	if runtime.app.logs == nil {
		return nil, errors.New("server logging is not enabled")
	}
	if err := onlyArguments(args); err != nil {
		return nil, err
	}
	return runtime.app.serverLogsSnapshot(ctx), nil
}

func mcpErrorResult(message string) *mcp.CallToolResult {
	payload := map[string]any{"error": message}
	data, _ := json.Marshal(payload)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
		IsError: true,
	}
}

func (runtime *mcpRuntime) callCatalog(args map[string]any) (any, error) {
	if err := onlyArguments(args, "kind", "id"); err != nil {
		return nil, err
	}
	kind, _ := args["kind"].(string)
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	switch kind {
	case "pages":
		pages, _ := runtime.catalogRaw["pages"].([]any)
		if id == "" {
			return map[string]any{"command": "pages", "pages": pages}, nil
		}
		for _, item := range pages {
			page, _ := item.(map[string]any)
			if page["id"] == id {
				return map[string]any{"command": "pages", "page": page}, nil
			}
		}
		return nil, fmt.Errorf("Unknown dashboard page: %s", id) //nolint:staticcheck // Shared MCP error contract uses sentence capitalization.
	case "queries":
		queries, _ := runtime.catalogRaw["queries"].([]any)
		if id == "" {
			return map[string]any{"command": "queries", "queries": queries}, nil
		}
		for _, item := range queries {
			entry, _ := item.(map[string]any)
			if entry["id"] == id {
				return map[string]any{"command": "query-info", "query": entry}, nil
			}
		}
		return nil, fmt.Errorf("Unknown dashboard query: %s", id) //nolint:staticcheck // Shared MCP error contract uses sentence capitalization.
	default:
		return nil, errors.New(`cao_catalog requires kind to be "pages" or "queries"`)
	}
}

func (runtime *mcpRuntime) callQuery(ctx context.Context, args map[string]any) (any, error) {
	if err := onlyArguments(args, "id", "parameters", "limit"); err != nil {
		return nil, err
	}
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("A dashboard query identifier is required") //nolint:staticcheck // Shared MCP error contract uses sentence capitalization.
	}
	catalogQuery, ok := runtime.findQuery(id)
	if !ok {
		return nil, fmt.Errorf("Unknown dashboard query: %s", id) //nolint:staticcheck // Shared MCP error contract uses sentence capitalization.
	}
	limit, err := runtime.queryLimit(args["limit"])
	if err != nil {
		return nil, err
	}
	definitions, filters, parameterValues, err := runtime.bindParameters(id, catalogQuery, args["parameters"])
	if err != nil {
		return nil, err
	}
	if !catalogQuery.Execution.Local {
		return map[string]any{
			"query": id, "rows": []model.Row{},
			"metadata": model.Metadata{
				"availability":     "unavailable",
				"completeness":     "unknown",
				"freshness":        "unknown",
				"query-diagnostic": catalogQuery.Execution.Reason,
				"requirements":     catalogQuery.Execution.Requirements,
			},
		}, nil
	}
	definition, ok := findDefinition(definitions, id)
	if !ok {
		return nil, fmt.Errorf("Unknown dashboard query: %s", id) //nolint:staticcheck // Shared MCP error contract uses sentence capitalization.
	}
	alias := "agent:" + id
	sourceName := alias + ":source"
	cloned := definition
	cloned.Name = sourceName
	boundedLimit := min(limit+1, query.MaxOutputRows)
	bounded := query.Definition{Name: alias, From: sourceName, Limit: &boundedLimit}
	if len(filters) > 0 {
		bounded.Filter = &query.Filter{Predicates: filters}
	}
	input := queryRequest{
		Aliases: []string{alias}, Queries: definitions,
		CompiledQueries: []query.Definition{cloned, bounded},
	}
	queryCtx, span := telemetry.Tracer().Start(ctx, telemetry.SpanQueryExecute)
	defer span.End()
	result, _, err := runtime.app.executeQuery(queryCtx, input, true)
	if err != nil {
		span.SetStatus(codes.Error, "query execution failed")
		return nil, err
	}
	result.Metrics.RateLimitCost = queryRateLimitCost(result.Metrics)
	if _, err := runtime.app.chargeQueryRateLimit(queryCtx, nil, result.Metrics.RateLimitCost); err != nil {
		span.SetStatus(codes.Error, "query rate limit exceeded")
		return nil, err
	}
	span.SetAttributes(queryTelemetryAttributes(input, result)...)
	span.SetStatus(codes.Ok, "")
	source := result.Sources[alias]
	rows := source.Rows
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	metadata := model.Metadata{
		"availability":  metadataString(source.Metadata, "availability", "unknown"),
		"completeness":  metadataString(source.Metadata, "completeness", "unknown"),
		"freshness":     metadataString(source.Metadata, "freshness", "unknown"),
		"as-of":         metadataString(source.Metadata, "as-of", ""),
		"returned-rows": len(rows),
		"limit":         limit,
	}
	if metadata["availability"] == "available" && len(rows) == 0 {
		metadata["availability"] = "empty"
	}
	if truncated {
		metadata["completeness"] = "partial"
	}
	if len(parameterValues) > 0 {
		metadata["parameters"] = parameterValues
	}
	return map[string]any{"query": id, "rows": rows, "metadata": metadata}, nil
}

func onlyArguments(args map[string]any, names ...string) error {
	for name := range args {
		if !slices.Contains(names, name) {
			return fmt.Errorf("unknown tool argument: %s", name)
		}
	}
	return nil
}

// queryLimitOutcome classifies how resolveQueryLimit produced its result, so
// callers can log the decision without exposing the caller-supplied limit
// value itself.
type queryLimitOutcome string

const (
	queryLimitOutcomeDefault  queryLimitOutcome = "default"
	queryLimitOutcomeClamped  queryLimitOutcome = "clamped-to-max"
	queryLimitOutcomeExact    queryLimitOutcome = "exact"
	queryLimitOutcomeRejected queryLimitOutcome = "rejected"
)

// resolveQueryLimit applies the cao_query tool's limit contract: a nil value
// selects defaultRows, a value at or above maxRows clamps to maxRows, and
// any other value must be a positive integer. It is a pure function
// extracted from mcpRuntime.queryLimit so the limit contract is testable
// against plain values instead of a constructed mcpRuntime.
func resolveQueryLimit(value any, defaultRows, maxRows int) (int, queryLimitOutcome, error) {
	if value == nil {
		return defaultRows, queryLimitOutcomeDefault, nil
	}
	number, ok := value.(float64)
	if !ok || math.IsInf(number, 0) || math.IsNaN(number) || math.Trunc(number) != number || number < 1 {
		return 0, queryLimitOutcomeRejected, errors.New("cao_query limit must be a positive integer")
	}
	if number >= float64(maxRows) {
		return maxRows, queryLimitOutcomeClamped, nil
	}
	return int(number), queryLimitOutcomeExact, nil
}

func (runtime *mcpRuntime) queryLimit(value any) (int, error) {
	limit, outcome, err := resolveQueryLimit(value, runtime.contract.Limits.DefaultQueryRows, runtime.contract.Limits.MaxQueryRows)
	if err != nil {
		mcpLog.Printf("cao_query limit rejected outcome=%s", outcome)
		return 0, err
	}
	return limit, nil
}

func (runtime *mcpRuntime) findQuery(id string) (agentQuery, bool) {
	for _, entry := range runtime.catalog.Queries {
		if entry.ID == id {
			return entry, true
		}
	}
	return agentQuery{}, false
}

func findDefinition(definitions []query.Definition, id string) (query.Definition, bool) {
	for _, definition := range definitions {
		if definition.Name == id {
			return definition, true
		}
	}
	return query.Definition{}, false
}

func metadataString(metadata model.Metadata, name, fallback string) string {
	if value, ok := metadata[name].(string); ok {
		return value
	}
	return fallback
}

// mcpContractHandler replaces SDK-normalized annotations with the authoritative
// contract annotations while leaving all protocol handling to the SDK.
type mcpContractHandler struct {
	next  http.Handler
	tools []mcpContractTool
}

func (handler *mcpContractHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	recorder := newBufferedResponse(response)
	handler.next.ServeHTTP(recorder, request)
	body := recorder.body
	if request.Header.Get("Mcp-Method") == "tools/list" && recorder.status == http.StatusOK {
		var message map[string]any
		if json.Unmarshal(body, &message) == nil {
			result, _ := message["result"].(map[string]any)
			tools, _ := result["tools"].([]any)
			byName := make(map[string]mcpContractTool, len(handler.tools))
			for _, tool := range handler.tools {
				byName[tool.Name] = tool
			}
			for _, item := range tools {
				tool, _ := item.(map[string]any)
				if declared, ok := byName[fmt.Sprint(tool["name"])]; ok {
					tool["annotations"] = declared.Annotations
				}
			}
			body, _ = json.Marshal(message)
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	recorder.flush(response, body)
}

type bufferedResponse struct {
	header http.Header
	status int
	body   []byte
}

func newBufferedResponse(response http.ResponseWriter) *bufferedResponse {
	return &bufferedResponse{header: response.Header(), status: http.StatusOK}
}

func (response *bufferedResponse) Header() http.Header    { return response.header }
func (response *bufferedResponse) WriteHeader(status int) { response.status = status }
func (response *bufferedResponse) Write(body []byte) (int, error) {
	response.body = append(response.body, body...)
	return len(body), nil
}
func (response *bufferedResponse) flush(target http.ResponseWriter, body []byte) {
	target.WriteHeader(response.status)
	_, _ = target.Write(body)
}
