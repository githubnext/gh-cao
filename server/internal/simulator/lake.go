package simulator

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
)

var lakeLog = logger.New("cao:simulator:lake")

type LakeResult struct {
	Repositories int `json:"repositories"`
	Runs         int `json:"runs"`
}

// lakeRequestRejectionStage identifies which of WriteLake's preconditions
// failed, so a misconfigured simulation request is diagnosable without
// logging the operator-supplied directory path itself.
type lakeRequestRejectionStage string

const (
	lakeRequestRejectionStageNone      lakeRequestRejectionStage = "none"
	lakeRequestRejectionStageHistory   lakeRequestRejectionStage = "history"
	lakeRequestRejectionStageHorizon   lakeRequestRejectionStage = "horizon"
	lakeRequestRejectionStageDirectory lakeRequestRejectionStage = "directory"
)

// validateLakeRequest applies WriteLake's precondition: a configured
// History, a horizon covered by History.Days, and an absolute output
// directory. It is a pure function extracted from WriteLake's combined
// inline condition, so each failure mode -- missing history, an
// out-of-range horizon, and a relative directory -- is independently
// testable without touching the filesystem.
func validateLakeRequest(history *History, horizonDays int, directory string) lakeRequestRejectionStage {
	switch {
	case history == nil:
		return lakeRequestRejectionStageHistory
	case horizonDays < 1 || horizonDays > history.Days:
		return lakeRequestRejectionStageHorizon
	case !filepath.IsAbs(directory):
		return lakeRequestRejectionStageDirectory
	default:
		return lakeRequestRejectionStageNone
	}
}

// WriteLake streams canonical synthetic evidence, not GitHub data or raw logs.
// It exercises the ordinary hash verification and native Postgres ingestion.
func (s Scenario) WriteLake(ctx context.Context, directory string, horizonDays int) (LakeResult, error) {
	s.defaults()
	if err := s.Validate(); err != nil {
		return LakeResult{}, err
	}
	if stage := validateLakeRequest(s.History, horizonDays, directory); stage != lakeRequestRejectionStageNone {
		lakeLog.Printf("synthetic lake request rejected stage=%s", stage)
		return LakeResult{}, errors.New("synthetic lake requires history, a covered horizon, and an absolute directory")
	}
	lakeLog.Printf("synthetic lake starting repositories=%d horizonDays=%d", s.Repositories, horizonDays)
	result := LakeResult{Repositories: s.Repositories, Runs: s.Repositories * horizonDays * s.History.RunsPerDay}
	err := writeSyntheticLake(ctx, directory, int64(result.Repositories*2+result.Runs), func(record func(string, model.Row) error) error {
		for index := 0; index < s.Repositories; index++ {
			name := repositoryName(index)
			repositoryID := "repository:" + name
			workflowID := "workflow:" + name + ":synthetic"
			if err := record("repositories", model.Row{
				"id": repositoryID, "owner": "simulator", "name": fmt.Sprintf("repo-%05d", index+1),
				"visibility": "public", "observedAt": s.History.AsOf,
			}); err != nil {
				return err
			}
			if err := record("workflows", model.Row{
				"id": workflowID, "repositoryId": repositoryID, "path": ".github/workflows/synthetic.md",
				"name": "Synthetic workflow", "state": "active", "role": "worker", "observedAt": s.History.AsOf,
			}); err != nil {
				return err
			}
			for offset := 0; offset < horizonDays*s.History.RunsPerDay; offset++ {
				run := s.History.Run(index, offset)
				created := run.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
				if err := record("runs", model.Row{
					"id": fmt.Sprintf("run:%d", run.ID), "repositoryId": repositoryID, "workflowId": workflowID,
					"owner": "simulator", "repository": fmt.Sprintf("repo-%05d", index+1),
					"workflowPath": ".github/workflows/synthetic.md", "githubRunId": fmt.Sprint(run.ID),
					"attempt": run.Attempt, "status": run.Status, "conclusion": run.Conclusion,
					"createdAt": created, "startedAt": created, "completedAt": created, "updatedAt": created,
					"observedAt": created,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return LakeResult{}, err
	}
	lakeLog.Printf("synthetic lake completed repositories=%d runs=%d", result.Repositories, result.Runs)
	return result, nil
}

func writeSyntheticLake(ctx context.Context, directory string, records int64, emit func(func(string, model.Row) error) error) error {
	if !filepath.IsAbs(directory) {
		return errors.New("synthetic lake requires an absolute directory")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	shards := filepath.Join(directory, "gh-aw-logs-runs")
	if err := os.MkdirAll(shards, 0o750); err != nil {
		return err
	}
	path := filepath.Join(shards, "synthetic.jsonl")
	// #nosec G304,G703 -- operator-selected local simulation output, never a GitHub API path.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	buffer := bufio.NewWriterSize(io.MultiWriter(file, hash), 256*1024)
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(map[string]any{
		"kind": "metadata", "schemaVersion": model.SchemaVersion, "ingestionVersion": 3,
		"phase": "runs", "records": records,
	}); err != nil {
		return err
	}
	record := func(collection string, row model.Row) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return encoder.Encode(map[string]any{"kind": "record", "collection": collection, "record": row})
	}
	if err := emit(record); err != nil {
		return err
	}
	if err := buffer.Flush(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	manifest, err := json.Marshal(map[string]string{"gh-aw-logs-runs/synthetic.jsonl": hex.EncodeToString(hash.Sum(nil))})
	if err != nil {
		return err
	}
	// #nosec G703 -- fixed output names under the operator-selected simulation directory.
	if err := os.WriteFile(filepath.Join(directory, "payload-hashes.json"), manifest, 0o600); err != nil {
		return err
	}
	// #nosec G703 -- fixed local simulation inventory, never remote input.
	if err := os.WriteFile(filepath.Join(directory, "inventory-sources.json"), []byte("{}\n"), 0o600); err != nil {
		return err
	}
	return nil
}
