package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// dashboardArtifactBacklogLimit bounds agentic runs waiting for artifacts.
const dashboardArtifactBacklogLimit = 5000

func dashboardArtifactBacklogPath(cache, repository string) string {
	return filepath.Join(cache, dashboardCoordinate(repository)+".artifact-backlog.json")
}

func readDashboardArtifactBacklog(cache, repository string) ([]map[string]any, error) {
	content, err := os.ReadFile(dashboardArtifactBacklogPath(cache, repository))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	runs := []map[string]any{}
	if err := json.Unmarshal(content, &runs); err != nil {
		return nil, errors.New("dashboard artifact backlog is not a valid run list")
	}
	return runs, nil
}

func writeDashboardArtifactBacklog(cache, repository string, runs []map[string]any) (err error) {
	content, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(cache, ".artifact-backlog-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() {
		if removeErr := os.Remove(name); !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := file.Write(content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(name, dashboardArtifactBacklogPath(cache, repository))
}

// dashboardArtifactQueue merges discovered agentic runs into the pending
// backlog, drops runs whose current attempt already has artifacts or that left
// the window, and orders the rest newest first.
func dashboardArtifactQueue(backlog, discovered []map[string]any, enriched map[string]map[string]any, cutoff time.Time) []map[string]any {
	byID := map[string]map[string]any{}
	for _, run := range slices.Concat(backlog, discovered) {
		byID[nativeScalar(run["databaseId"])] = run
	}
	queue := make([]map[string]any, 0, len(byID))
	for id, run := range byID {
		created, err := time.Parse(time.RFC3339, nativeText(run["createdAt"]))
		if err != nil || created.Before(cutoff) {
			continue
		}
		if record := enriched[id]; record != nil && nativeScalar(record["run_attempt"]) == nativeScalar(run["attempt"]) {
			continue
		}
		queue = append(queue, run)
	}
	slices.SortFunc(queue, func(left, right map[string]any) int {
		a, _ := strconv.ParseInt(nativeScalar(left["databaseId"]), 10, 64)
		b, _ := strconv.ParseInt(nativeScalar(right["databaseId"]), 10, 64)
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		}
		return 0
	})
	if len(queue) > dashboardArtifactBacklogLimit {
		queue = queue[:dashboardArtifactBacklogLimit]
	}
	return queue
}
