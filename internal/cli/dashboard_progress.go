package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// This bounded receipt is acquisition telemetry, independent of canonical publication.
type dashboardProgress struct {
	mu    sync.Mutex
	cache string
	seen  map[string]bool
	value struct {
		PassID       string `json:"passId"`
		Repository   string `json:"repository"`
		Phase        string `json:"phase"`
		ReceivedRuns int    `json:"receivedRuns"`
		StartedAt    string `json:"startedAt"`
		UpdatedAt    string `json:"updatedAt"`
	}
}

func newDashboardProgress(cache string, now time.Time) *dashboardProgress {
	progress := &dashboardProgress{cache: cache, seen: map[string]bool{}}
	progress.value.PassID = now.Format(time.RFC3339Nano)
	progress.value.StartedAt = progress.value.PassID
	return progress
}

func (p *dashboardProgress) receive(repository string, record map[string]any) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := repository + ":" + nativeScalar(record["run_id"]) + ":" + nativeScalar(record["run_attempt"])
	if p.seen[key] {
		return nil
	}
	p.seen[key] = true
	p.value.ReceivedRuns++
	return p.publishLocked("receiving", repository)
}

func (p *dashboardProgress) publish(phase, repository string) (err error) {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishLocked(phase, repository)
}

func (p *dashboardProgress) publishLocked(phase, repository string) (err error) {
	p.value.Phase, p.value.Repository = phase, repository
	p.value.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	content, err := json.Marshal(p.value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(p.cache, ".ingestion-progress-")
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
	return os.Rename(name, filepath.Join(p.cache, "ingestion-progress.json"))
}
