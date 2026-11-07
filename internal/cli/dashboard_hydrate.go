package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardingest"
)

func hydrateDashboardEvidence(ctx context.Context, evidence []dashboardRepositoryEvidence, batch map[string][]map[string]any, now time.Time) (map[string][]map[string]any, error) {
	result := map[string][]map[string]any{}
	indexes := map[string]map[string]int{}
	for store, rows := range batch {
		result[store] = append(result[store], rows...)
		indexes[store] = map[string]int{}
		for index, row := range rows {
			indexes[store][nativeText(row["id"])] = index
		}
	}
	result["runs"] = nil
	indexes["runs"] = map[string]int{}
	for _, source := range evidence {
		var jsonl strings.Builder
		artifacts := []map[string]any{}
		selected := map[string]int{}
		base := map[string]map[string]any{}
		workflows := map[string]dashboardWorkflow{}
		for _, workflow := range source.Workflows {
			workflows[strconv.FormatInt(workflow.ID, 10)] = workflow
		}
		rawByID := map[string]map[string]any{}
		for _, raw := range source.Runs {
			rawByID[nativeScalar(raw["databaseId"])] = raw
		}
		appendEnvelope := func(envelope map[string]any) error {
			content, err := json.Marshal(envelope)
			if err != nil {
				return err
			}
			if jsonl.Len()+len(content)+1 > 32<<20 {
				return errors.New("selected gh-aw cache evidence exceeds the 32 MiB hydration budget")
			}
			jsonl.Write(content)
			jsonl.WriteByte('\n')
			return nil
		}
		artifactBytes := 0
		for _, run := range batch["runs"] {
			if run["repositoryFullName"] != source.Repository {
				continue
			}
			id := nativeScalar(run["githubRunId"])
			attempt := int(nativeNumber(run["attempt"]))
			raw := rawByID[id]
			workflow := workflows[nativeScalar(raw["workflowDatabaseId"])]
			enriched := source.Enriched[id]
			matching := enriched != nil && nativeScalar(enriched["run_attempt"]) == strconv.Itoa(attempt)
			value := map[string]any{}
			if matching {
				for key, field := range enriched {
					value[key] = field
				}
			}
			for from, to := range map[string]string{
				"createdAt": "created_at", "startedAt": "started_at", "updatedAt": "updated_at",
				"status": "status", "conclusion": "conclusion", "event": "event", "url": "url",
				"displayTitle": "display_title", "headBranch": "branch", "headSha": "head_sha",
			} {
				if field, exists := raw[from]; exists {
					value[to] = field
				}
			}
			value["run_id"], value["run_attempt"] = id, strconv.Itoa(attempt)
			value["organization"] = strings.Split(source.Repository, "/")[0]
			value["repository"] = source.Repository
			value["workflow_name"], value["workflow_path"] = workflow.Name, workflow.Path
			if err := appendEnvelope(map[string]any{"schema_version": 5, "kind": "run", "run": value}); err != nil {
				return nil, err
			}
			selected[id], base[nativeText(run["id"])] = attempt, run
			if matching && source.Directory != "" {
				files, err := readDashboardArtifacts(ctx, source.Directory, id, &artifactBytes)
				if err != nil {
					return nil, err
				}
				if len(files) > 0 {
					parts := strings.Split(source.Repository, "/")
					artifacts = append(artifacts, map[string]any{
						"observedAt": now.Format(time.RFC3339Nano),
						"repository": map[string]any{"githubId": source.GithubID, "owner": parts[0], "name": parts[1]},
						"workflow":   map[string]any{"githubId": workflow.ID, "name": workflow.Name, "path": workflow.Path, "state": workflow.State},
						"run": map[string]any{
							"githubRunId": id, "attempt": attempt, "event": run["event"], "status": run["status"],
							"conclusion": run["conclusion"], "createdAt": run["createdAt"], "startedAt": run["startedAt"],
							"completedAt": run["completedAt"], "url": run["runLink"],
						},
						"files": files,
					})
				}
			}
		}
		for _, item := range source.SafeOutputs {
			attempt, exists := selected[nativeScalar(item["run_id"])]
			if !exists || !dashboardSafeOutputMatchesAttempt(item, source.Enriched[nativeScalar(item["run_id"])], attempt) {
				continue
			}
			if err := appendEnvelope(map[string]any{"schema_version": 5, "kind": "safe_output_item", "safe_output": item}); err != nil {
				return nil, err
			}
		}
		if jsonl.Len() == 0 {
			continue
		}
		normalized, err := dashboardingest.Hydrate(ctx, dashboardingest.Input{JSONL: jsonl.String(), Artifacts: artifacts})
		if err != nil {
			return nil, err
		}
		for store, rows := range normalized {
			if indexes[store] == nil {
				indexes[store] = map[string]int{}
			}
			for _, row := range rows {
				id := nativeText(row["id"])
				if id == "" {
					return nil, errors.New("normalizer emitted a missing canonical identity")
				}
				index, present := indexes[store][id]
				if store == "repositories" || store == "workflows" {
					if !present {
						return nil, errors.New("normalizer emitted inventory outside selected repositories")
					}
					for key, value := range row {
						if _, present := result[store][index][key]; !present {
							result[store][index][key] = value
						}
					}
					continue
				}
				if store == "issues" && nativeText(row["owner"])+"/"+nativeText(row["repository"]) != source.Repository {
					continue
				}
				if store == "runs" {
					fresh := base[nativeText(row["id"])]
					if fresh == nil {
						return nil, errors.New("normalizer emitted a run outside selected observations")
					}
					for key, value := range fresh {
						if key != "provenance" && key != "observedAt" {
							row[key] = value
						}
					}
				}
				if !present {
					indexes[store][id] = len(result[store])
					result[store] = append(result[store], row)
				} else if !reflect.DeepEqual(result[store][index], row) {
					return nil, fmt.Errorf("conflicting canonical %s identity across repository observations", store)
				}
			}
		}
	}
	return result, nil
}

func dashboardSafeOutputMatchesAttempt(item, enriched map[string]any, attempt int) bool {
	if enriched == nil || nativeScalar(enriched["run_attempt"]) != strconv.Itoa(attempt) {
		return false
	}
	if item["run_attempt"] != nil {
		return nativeScalar(item["run_attempt"]) == strconv.Itoa(attempt)
	}
	copy := make(map[string]any, len(item))
	for key, value := range item {
		if key != "run_id" {
			copy[key] = value
		}
	}
	return slices.ContainsFunc(nativeObjects(enriched["safe_outputs"]), func(nested map[string]any) bool {
		return reflect.DeepEqual(copy, nested)
	})
}

func readDashboardArtifacts(ctx context.Context, directory, id string, total *int) (files []map[string]any, err error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	run, err := root.OpenRoot("run-" + id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open scoped gh-aw run artifacts: %w", err)
	}
	defer func() { err = errors.Join(err, run.Close()) }()
	paths := []string{}
	entries := 0
	err = fs.WalkDir(run.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > 10000 {
			return errors.New("gh-aw run artifacts exceed the 10000-file inspection budget")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("gh-aw run artifacts must not contain symbolic links")
		}
		if !entry.IsDir() && dashboardArtifactPath(name) {
			paths = append(paths, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	unified := slices.ContainsFunc(paths, func(name string) bool { return path.Base(name) == "aw_session.jsonl" })
	for _, name := range paths {
		if unified && (path.Base(name) == "gateway.jsonl" || path.Base(name) == "rpc-messages.jsonl" ||
			strings.Contains(name, "firewall") || strings.Contains(name, "copilot-session-state")) {
			continue
		}
		file, err := run.Open(name)
		if err != nil {
			return nil, err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return nil, errors.Join(statErr, file.Close(), errors.New("gh-aw artifact must be a regular file within the 16 MiB budget"))
		}
		content, readErr := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		if err := errors.Join(readErr, file.Close()); err != nil {
			return nil, err
		}
		*total += len(content)
		if len(content) > 16<<20 || *total > 32<<20 {
			return nil, errors.New("gh-aw artifacts exceed the 32 MiB repository hydration budget")
		}
		files = append(files, map[string]any{"path": "run-" + id + "/" + name, "content": string(content)})
	}
	return files, nil
}

func dashboardArtifactPath(name string) bool {
	switch path.Base(name) {
	case "aw_session.jsonl", "gateway.jsonl", "rpc-messages.jsonl", "evals.jsonl",
		"grader_results.json", "grader_manifest.json", "assignments.json":
		return true
	case "audit.jsonl":
		return strings.Contains(name, "firewall")
	case "events.jsonl":
		return strings.Contains(name, "copilot-session-state/")
	}
	return false
}
