package tokenbroker

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

// Only fixed operation names and outcomes cross this boundary, never credentials,
// verified identities, policy contents or upstream errors.
func observeOperation[T any](ctx context.Context, name string, operation func(context.Context) (T, error)) (T, error) {
	ctx, span := telemetry.Tracer().Start(ctx, name)
	defer span.End()
	result, err := operation(ctx)
	outcome, severity := "success", otelLog.SeverityInfo
	if errors.Is(err, admission.ErrDenied) {
		outcome = "denied"
	} else if err != nil {
		outcome, severity = "unavailable", otelLog.SeverityError
		span.SetStatus(codes.Error, "token broker dependency unavailable")
	}
	var record otelLog.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(severity)
	record.SetBody(attribute.StringValue("token broker operation completed"))
	record.AddAttributes(attribute.String("operation", name), attribute.String("outcome", outcome))
	global.GetLoggerProvider().Logger("cao:tokenbroker").Emit(ctx, record)
	return result, err
}
