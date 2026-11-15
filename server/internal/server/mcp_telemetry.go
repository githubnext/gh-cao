package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var mcpTelemetryLog = logger.New("cao:server:mcp-telemetry")

var mcpVersionPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

const (
	mcpMethodNameKey      = "mcp.method.name"
	mcpProtocolVersionKey = "mcp.protocol.version"
	genAIToolNameKey      = "gen_ai.tool.name"
	genAIOperationNameKey = "gen_ai.operation.name"
	errorTypeKey          = "error.type"
	rpcStatusCodeKey      = "rpc.response.status_code"
)

func mcpServerTelemetry() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			safeMethod := safeMCPMethod(method)
			spanName := safeMethod
			attributes := []attribute.KeyValue{
				attribute.String(mcpMethodNameKey, safeMethod),
				attribute.String("network.transport", "tcp"),
				attribute.String("network.protocol.name", "http"),
			}
			if toolName := mcpToolName(request); toolName != "" {
				spanName += " " + toolName
				attributes = append(attributes,
					attribute.String(genAIToolNameKey, toolName),
					attribute.String(genAIOperationNameKey, "execute_tool"),
				)
			}
			if protocolVersion := mcpRequestProtocolVersion(request); mcpVersionPattern.MatchString(protocolVersion) {
				attributes = append(attributes, attribute.String(mcpProtocolVersionKey, protocolVersion))
			}
			parentContext, links := mcpParentContext(ctx, request)
			parentContext, span := telemetry.Tracer().Start(parentContext, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attributes...),
				trace.WithLinks(links...),
			)
			defer span.End()

			result, err := next(parentContext, method, request)
			if err != nil {
				recordMCPServerError(span, err)
			} else if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult.IsError {
				span.SetAttributes(attribute.String(errorTypeKey, "tool_error"))
				span.SetStatus(codes.Error, "MCP tool call failed")
			}
			return result, err
		}
	}
}

func mcpParentContext(ambient context.Context, request mcp.Request) (context.Context, []trace.Link) {
	return mcpTraceContext(ambient, mcpMetadata(request.GetParams()))
}

func mcpMetadata(params mcp.Params) map[string]any {
	if params == nil {
		return nil
	}
	value := reflect.ValueOf(params)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return params.GetMeta()
}

func mcpTraceContext(ambient context.Context, metadata map[string]any) (context.Context, []trace.Link) {
	traceparent, _ := metadata["traceparent"].(string)
	parent := trace.ContextWithSpanContext(ambient, trace.SpanContext{})
	parent = telemetry.ExtractTraceparent(parent, traceparent)
	if spanContext := trace.SpanContextFromContext(ambient); spanContext.IsValid() {
		return parent, []trace.Link{{SpanContext: spanContext.WithTraceState(trace.TraceState{})}}
	}
	return parent, nil
}

// mcpServerErrorClassification is the pure decision recordMCPServerError acts
// on: which span attributes to set, and whether the error is a caller error
// (in which case the span keeps an unset/success status rather than being
// marked as failed).
type mcpServerErrorClassification struct {
	StatusCodeAttribute string
	ErrorTypeAttribute  string
	IsCallerError       bool
}

// classifyMCPServerError derives the telemetry attributes for a failed MCP
// request without touching the span, so the JSON-RPC error code, caller vs.
// server error classification, and dynamic error type keep passing the same
// decision-relevant inputs a test can assert on directly.
func classifyMCPServerError(err error) mcpServerErrorClassification {
	var rpcError *jsonrpc.Error
	if errors.As(err, &rpcError) {
		code := strconv.FormatInt(rpcError.Code, 10)
		if isMCPCallerError(rpcError.Code) {
			return mcpServerErrorClassification{StatusCodeAttribute: code, IsCallerError: true}
		}
		return mcpServerErrorClassification{StatusCodeAttribute: code, ErrorTypeAttribute: code}
	}
	return mcpServerErrorClassification{ErrorTypeAttribute: fmt.Sprintf("%T", err)}
}

func recordMCPServerError(span trace.Span, err error) {
	classification := classifyMCPServerError(err)
	if classification.StatusCodeAttribute != "" {
		span.SetAttributes(attribute.String(rpcStatusCodeKey, classification.StatusCodeAttribute))
	}
	if classification.IsCallerError {
		mcpTelemetryLog.Printf("mcp server error classified as caller_error")
		return
	}
	if classification.ErrorTypeAttribute != "" {
		span.SetAttributes(attribute.String(errorTypeKey, classification.ErrorTypeAttribute))
	}
	mcpTelemetryLog.Printf("mcp server error classified as server_error")
	span.SetStatus(codes.Error, "MCP request failed")
}

func isMCPCallerError(code int64) bool {
	switch code {
	case jsonrpc.CodeParseError, jsonrpc.CodeInvalidRequest, jsonrpc.CodeMethodNotFound,
		jsonrpc.CodeInvalidParams:
		return true
	default:
		return false
	}
}

func safeMCPMethod(method string) string {
	switch method {
	case "initialize", "ping", "tools/list", "tools/call", "resources/list",
		"resources/read", "resources/templates/list", "prompts/list", "prompts/get",
		"completion/complete", "logging/setLevel", "notifications/initialized",
		"notifications/cancelled", "notifications/progress":
		return method
	default:
		return "other"
	}
}

func mcpToolName(request mcp.Request) string {
	var name string
	switch params := request.GetParams().(type) {
	case *mcp.CallToolParams:
		name = params.Name
	case *mcp.CallToolParamsRaw:
		name = params.Name
	}
	if name == "cao_catalog" || name == "cao_query" || name == "cao_logs" || name == "cao_session_query" {
		return name
	}
	return ""
}

func mcpRequestProtocolVersion(request mcp.Request) string {
	versioned, ok := request.(interface{ ProtocolVersion() string })
	if !ok {
		return ""
	}
	return versioned.ProtocolVersion()
}
