package collect

import (
	"context"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func testQueueLength(ctx context.Context, store operational.TaskQueue, name string) (int64, error) {
	stats, err := store.QueueStats(ctx, name, "")
	return stats.Length, err
}
