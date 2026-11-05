package collect

import (
	"context"
	"errors"
	"time"
)

func (q Queue) nativeHealth(ctx context.Context, success, dead bool) error {
	if q.Metrics == nil {
		return nil
	}
	if success {
		return errors.Join(
			q.Metrics.IncrementIngestionCounter(ctx, "collectionSucceeded"),
			q.Metrics.RecordIngestionHealthEvent(ctx, "success", "", time.Now().UTC()))
	}
	counter := "collectionRetried"
	if dead {
		counter = "collectionDeadLettered"
	}
	return errors.Join(
		q.Metrics.IncrementIngestionCounter(ctx, "collectionFailed"),
		q.Metrics.IncrementIngestionCounter(ctx, counter),
		q.Metrics.RecordIngestionHealthEvent(ctx, "failure", "collection", time.Now().UTC()))
}
