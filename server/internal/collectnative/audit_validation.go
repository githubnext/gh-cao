package collectnative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type StepLogArchive struct {
	Files map[string][]byte
	Gap   string
}

// StepLogProvider retrieves the step-file archive for one exact run attempt.
// Job stdout is not an equivalent substitute: one issue represents one file,
// regardless of how many annotations that step emitted.
type StepLogProvider interface {
	StepLogs(context.Context, Run) (StepLogArchive, error)
}

func (g GitHub) StepLogs(ctx context.Context, run Run) (StepLogArchive, error) {
	if run.Validate() != nil {
		return StepLogArchive{}, ErrIdentity
	}
	content, headers, status, err := g.get(ctx, run.InstallationID, fmt.Sprintf(
		"repos/%s/actions/runs/%d/attempts/%d/logs", run.Repository, run.RunID, run.Attempt))
	switch {
	case ctx.Err() != nil:
		return StepLogArchive{}, ctx.Err()
	case status == http.StatusNotFound || status == http.StatusGone:
		return StepLogArchive{Gap: "workflow-step-logs-unavailable"}, nil
	case err != nil:
		return StepLogArchive{}, err
	case status == http.StatusFound:
		content, err = g.signedDownload(ctx, headers)
		if err != nil {
			return StepLogArchive{}, err
		}
	}
	files, _, err := unpackSelected(ctx, content, g.Limits.defaults(), func(name string) bool {
		return strings.HasSuffix(name, ".txt") && len(strings.Split(name, "/")) <= 2
	})
	return StepLogArchive{Files: files}, err
}

func acquireValidation(ctx context.Context, provider Provider, run Run, fields map[string]any, limits Limits, bytesUsed *int) ([]string, error) {
	if fields["conclusion"] != "failure" {
		return nil, nil
	}
	source, ok := provider.(StepLogProvider)
	if !ok {
		return []string{"workflow-step-logs-unavailable"}, nil
	}
	archive, err := source.StepLogs(ctx, run)
	if err != nil {
		return nil, err
	}
	if archive.Gap != "" {
		return []string{archive.Gap}, nil
	}
	if len(archive.Files) > limits.defaults().Entries {
		return nil, ErrCapacity
	}
	issues := []any{}
	lastStep, lastStepNonempty, work := 0, false, 0
	for _, name := range slices.Sorted(maps.Keys(archive.Files)) {
		number, _, _ := strings.Cut(path.Base(name), "_")
		step, err := strconv.Atoi(number)
		if err != nil || step <= 0 {
			continue
		}
		content := archive.Files[name]
		*bytesUsed += len(content)
		if *bytesUsed > limits.defaults().Bytes {
			return nil, ErrCapacity
		}
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			return []string{"invalid-workflow-step-log"}, nil
		}
		found := false
		err = scanEvidence(ctx, content, limits, func(_ int, line []byte) error {
			work++
			if work > limits.defaults().Entries {
				return ErrCapacity
			}
			if bytes.Contains(line, []byte("##[error]")) && !agentToolResultAnnotation(line) {
				found = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if step > lastStep {
			lastStep, lastStepNonempty = step, len(bytes.TrimSpace(content)) > 0
		}
		if found {
			// Step names and diagnostics can contain arbitrary payloads.
			// Keep stable location identity, not their contents.
			digest := sha256.Sum256([]byte(name))
			issues = append(issues, map[string]any{"type": "step_failure", "file": fmt.Sprintf("actions-step:%x", digest[:12]),
				"message": "Actions step reported an error", "step_number": step})
		}
	}
	audit := object(fields["audit"])
	if audit == nil {
		audit = map[string]any{}
		fields["audit"] = audit
	}
	audit["errors"] = issues
	audit["native_last_step_nonempty"] = lastStepNonempty
	return nil, nil
}

func agentToolResultAnnotation(line []byte) bool {
	_, raw, found := bytes.Cut(line, []byte("##[error]"))
	if !found {
		return false
	}
	var event map[string]any
	if decode(bytes.TrimSpace(raw), &event) != nil || event["type"] != "user" {
		return false
	}
	content := array(object(event["message"])["content"])
	if len(content) == 0 {
		return false
	}
	for _, block := range content {
		if object(block)["type"] != "tool_result" {
			return false
		}
	}
	return true
}

func finishValidation(ctx context.Context, fields map[string]any, files map[string][]byte, limits Limits) error {
	if fields["conclusion"] != "failure" {
		return nil
	}
	audit := object(fields["audit"])
	if audit == nil {
		audit = map[string]any{}
		fields["audit"] = audit
	}
	if len(array(audit["errors"])) == 0 {
		kind := ""
		for _, name := range candidates(files, "agent-stdio.log") {
			if engineEvidencePath(name) && len(bytes.TrimSpace(files[name])) > 0 {
				if len(files[name]) > limits.defaults().Bytes {
					return ErrCapacity
				}
				if !utf8.Valid(files[name]) {
					return errors.New("invalid agent failure excerpt")
				}
				kind = "agent_failure"
				break
			}
		}
		if kind == "" && audit["native_last_step_nonempty"] == true {
			kind = "step_failure"
		}
		if kind != "" {
			audit["errors"] = []any{map[string]any{"type": kind, "message": "Failed workflow execution has diagnostic output"}}
		}
	}
	delete(audit, "native_last_step_nonempty")
	return ctx.Err()
}
