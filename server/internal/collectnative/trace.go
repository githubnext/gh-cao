package collectnative

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// SessionTraceAdmission owns approval, projection, privacy and durable capture.
// Acquisition passes only exact-attempt input verified against its artifact
// identity envelope; it does not select trace policy or delivery scope.
type SessionTraceAdmission interface {
	CaptureSession(context.Context, string, string, int64, time.Time, []byte, string) error
}

type TraceDestination struct {
	Admission SessionTraceAdmission
}

func (d TraceDestination) Capture(ctx context.Context, run Run, content []byte, gap string) error {
	if d.Admission == nil {
		return errors.New("native trace destination requires durable admission")
	}
	if err := run.Validate(); err != nil {
		return err
	}
	// Sampling frames admit completed attempts only, matching the existing
	// trace contract. In-progress acquisition is not a trace source gap.
	if run.Fields["status"] != "completed" {
		return nil
	}
	recorded := firstText(run.Fields["completed_at"], run.Fields["updated_at"])
	completed, err := time.Parse(time.RFC3339Nano, recorded)
	if err != nil {
		return errors.New("native trace destination requires a recorded completion clock")
	}
	return d.Admission.CaptureSession(ctx, run.Repository, strconv.FormatInt(run.RunID, 10),
		int64(run.Attempt), completed, content, gap)
}
