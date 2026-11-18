package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (g *dashboardColdStartGitHub) stressArtifacts(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/repos/octo/api/actions/artifacts/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/zip") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/zip")
	if _, ok := g.expected[id]; !ok {
		g.reject(w, "artifact requested for an unselected run")
		return true
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	record := map[string]any{
		"run_id": id, "run_attempt": "1", "repository": "octo/api",
		"workflow_path":       ".github/workflows/agent.lock.yml",
		"token_usage_summary": map[string]any{"total_aic": 1, "total_input_tokens": 100},
	}
	metadata, err := writer.Create("aw_info.json")
	if err == nil {
		err = json.NewEncoder(metadata).Encode(record)
	}
	var session io.Writer
	if err == nil {
		session, err = writer.Create("usage/aw_session.jsonl")
	}
	if err == nil {
		encoder := json.NewEncoder(session)
		events := []map[string]any{
			{"type": "session.format", "data": map[string]any{"version": 1}},
			{"type": "workflow.info", "data": map[string]any{"runId": id, "engineId": "copilot"}},
			{"type": "mcp.tool_call", "data": map[string]any{
				"serverName": "github", "toolName": "get_issue", "toolCallId": "call-" + id, "status": "success",
			}},
		}
		// Global Issue identity is intentionally observed by two different Runs.
		// This must hit the ownership guard between streamed publications.
		if id == "10200" || id == "10205" {
			events = append(events, map[string]any{"type": "safe_output.result", "data": map[string]any{
				"type": "create_issue", "repo": "octo/api", "number": 42, "url": "https://github.com/octo/api/issues/42",
			}})
		}
		for _, event := range events {
			if err = encoder.Encode(event); err != nil {
				break
			}
		}
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		g.t.Errorf("synthetic artifact: %v", err)
		http.Error(w, "synthetic artifact generation failed", http.StatusInternalServerError)
		return true
	}
	g.mutex.Lock()
	g.artifactRequests++
	g.mutex.Unlock()
	w.Header().Set("Content-Type", "application/zip")
	_, _ = w.Write(archive.Bytes())
	return true
}

func (g *dashboardColdStartGitHub) stressLogs(ctx context.Context, request dashboardLogRequest) error {
	if request.Repository != "octo/api" || request.Count < 1 || request.Count > 200 ||
		request.StartDate == "" || request.Directory == "" {
		return fmt.Errorf("unbounded synthetic artifact request")
	}
	return downloadDashboardLogs(ctx, request, func(ctx context.Context, _ []string, output io.Writer) error {
		encoder := json.NewEncoder(output)
		var summary []map[string]any
		for _, run := range g.runs {
			id := nativeScalar(run["id"])
			number, _ := strconv.ParseInt(id, 10, 64)
			if _, selected := g.expected[id]; !selected || number <= request.AfterRunID || number >= request.BeforeRunID {
				continue
			}
			if len(summary) >= request.Count {
				return fmt.Errorf("synthetic artifact selection exceeded requested count")
			}
			content, err := g.get(ctx, "/repos/octo/api/actions/artifacts/"+id+"/zip")
			if err != nil {
				return err
			}
			archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
			if err != nil {
				return err
			}
			record := map[string]any{}
			for _, file := range archive.File {
				if file.Name != "aw_info.json" && file.Name != "usage/aw_session.jsonl" {
					return fmt.Errorf("unexpected synthetic artifact member")
				}
				source, err := file.Open()
				if err != nil {
					return err
				}
				data, readErr := io.ReadAll(io.LimitReader(source, 1<<20))
				closeErr := source.Close()
				if readErr != nil {
					return readErr
				}
				if closeErr != nil {
					return closeErr
				}
				if file.Name == "aw_info.json" {
					if err := json.Unmarshal(data, &record); err != nil {
						return err
					}
				} else {
					destination := filepath.Join(request.Directory, "run-"+id, filepath.FromSlash(file.Name))
					if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
						return err
					}
					if err := os.WriteFile(destination, data, 0o600); err != nil {
						return err
					}
				}
			}
			summary = append(summary, record)
			if err := encoder.Encode(map[string]any{"type": "run", "run": record}); err != nil {
				return err
			}
		}
		if err := encoder.Encode(map[string]any{"type": "summary", "data": map[string]any{"runs": summary}}); err != nil {
			return err
		}
		return encoder.Encode(map[string]any{"type": "complete"})
	})
}
