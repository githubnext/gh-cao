package collectnative

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Compact preserves Activity's raw JSONL compaction semantics, including byte
// identity for enriched-run deduplication and source-order/null preservation.
// Input must be a stable, replayable stream; neither pass creates local files.
// Ownership is checked before any output is written.
func Compact(ctx context.Context, repository string, input io.ReadSeeker, output io.Writer, limits Limits) error {
	if !coordinate.MatchString(repository) {
		return ErrIdentity
	}
	limits = limits.defaults()
	agentic := map[string]bool{}
	identities := map[string]bool{}
	visit := func(f func(map[string]any, []byte) error) error {
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return err
		}
		scanner := bufio.NewScanner(io.LimitReader(input, int64(limits.Bytes)+1))
		scanner.Buffer(make([]byte, min(4096, limits.LineBytes)), limits.LineBytes)
		total, entries := 0, 0
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			line := scanner.Bytes()
			total += len(line) + 1
			if total > limits.Bytes {
				return ErrCapacity
			}
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			entries++
			if entries > limits.Entries {
				return ErrCapacity
			}
			var envelope map[string]any
			if err := decode(line, &envelope); err != nil || envelope == nil {
				return errors.New("invalid compaction JSONL")
			}
			if err := f(envelope, line); err != nil {
				return err
			}
		}
		return scanner.Err()
	}
	if err := visit(func(envelope map[string]any, _ []byte) error {
		request := object(envelope["request"])
		run := object(envelope["run"])
		for _, candidate := range []any{request["repository"], run["repository_full_name"]} {
			if candidate == nil || candidate == "" {
				continue
			}
			if value, ok := candidate.(string); !ok || strings.ToLower(value) != repository {
				return ErrIdentity
			}
		}
		if run != nil {
			if run["repository"] != nil && repositoryOf(run) != repository {
				return ErrIdentity
			}
			id := scalar(run["run_id"])
			if id == "" {
				id = scalar(run["database_id"])
			}
			identities[id] = true
			if strings.HasSuffix(scalar(run["workflow_path"]), ".lock.yml") && id != "" {
				agentic[id] = true
			}
		}
		if len(identities) > limits.Runs {
			return ErrCapacity
		}
		return nil
	}); err != nil {
		return err
	}
	digests := map[[32]byte]bool{}
	return visit(func(envelope map[string]any, line []byte) error {
		switch scalar(envelope["kind"]) {
		case "run", "token_efficiency_run_context":
			if !strings.HasSuffix(scalar(object(envelope["run"])["workflow_path"]), ".lock.yml") {
				return nil
			}
			if scalar(envelope["kind"]) == "run" {
				digest := sha256.Sum256(line)
				if digests[digest] {
					return nil
				}
				digests[digest] = true
			}
		case "safe_output_item":
			if !agentic[scalar(object(envelope["safe_output"])["run_id"])] {
				return nil
			}
		case "token_efficiency_observation", "token_efficiency_lifecycle_observation":
			if !agentic[scalar(object(envelope["observation"])["optimizerRunId"])] {
				return nil
			}
		case "workflow_runs":
			if payload, ok := envelope["payload"].([]any); ok {
				retained := make([]any, 0, len(payload))
				for _, item := range payload {
					if agentic[scalar(object(item)["databaseId"])] {
						retained = append(retained, item)
					}
				}
				if len(retained) == 0 {
					return nil
				}
				if len(retained) != len(payload) {
					envelope["payload"] = retained
					var err error
					line, err = json.Marshal(envelope)
					if err != nil {
						return err
					}
				}
			}
		}
		if _, err := output.Write(line); err != nil {
			return err
		}
		_, err := io.WriteString(output, "\n")
		return err
	})
}
