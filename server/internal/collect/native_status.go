package collect

import (
	"context"
	"time"
)

type nativeQueueStatus struct {
	depth, pending, dead int64
	oldest               time.Duration
}

func (q Queue) nativeStatus(ctx context.Context) (nativeQueueStatus, error) {
	var result nativeQueueStatus
	for _, stream := range []struct{ ready, delayed, dead string }{
		{runTaskStream, delayedRunTaskSet, runDeadLetterStream},
		{runEnumerationStream, delayedRunEnumerationSet, ""},
		{nativeControlStream, nativeControlDelayed, nativeControlDead},
	} {
		stats, err := q.Tasks.QueueStats(ctx, stream.ready, q.group())
		if err != nil {
			return result, err
		}
		delayed, err := q.Tasks.DelayedDepth(ctx, stream.delayed)
		if err != nil {
			return result, err
		}
		result.depth += stats.Backlog + delayed
		result.pending += stats.Pending
		result.oldest = max(result.oldest, stats.OldestPendingAge)
		if stream.dead != "" {
			dead, err := q.Tasks.QueueStats(ctx, stream.dead, "")
			if err != nil {
				return result, err
			}
			result.dead += dead.Length
		}
	}
	return result, nil
}
