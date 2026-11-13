package cli

import (
	"context"
	"time"
)

type dashboardCollectionTimings struct {
	phases         map[string]time.Duration
	publications   int
	ingestions     int
	githubCommands int
}

func (t *dashboardCollectionTimings) record(phase string, start time.Time) {
	if t != nil {
		t.phases[phase] += time.Since(start)
	}
}

func (t *dashboardCollectionTimings) total() time.Duration {
	var total time.Duration
	for _, elapsed := range t.phases {
		total += elapsed
	}
	return total
}

func (t *dashboardCollectionTimings) milliseconds() map[string]float64 {
	result := map[string]float64{}
	for phase, elapsed := range t.phases {
		result[phase] = float64(elapsed) / float64(time.Millisecond)
	}
	return result
}

func (options dashboardOptions) publishSnapshot(ctx context.Context, cache string, batch map[string][]map[string]any) error {
	start := time.Now()
	err := writeDashboardSnapshot(ctx, cache, batch, true)
	options.timings.record("snapshot", start)
	if err == nil && options.timings != nil {
		options.timings.publications++
	}
	return err
}
