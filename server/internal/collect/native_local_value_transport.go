package collect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

func writeNativeValueInputs(workspace string, inputs postgresx.NativeValueInputs) error {
	directory := filepath.Join(workspace, "gh-aw-logs-shards")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	inventory, err := json.Marshal(inputs.Inventory)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(workspace, "inventory-sources.json"), inventory, 0o600); err != nil {
		return err
	}
	for _, input := range []struct {
		name, kind string
		rows       []model.Row
	}{
		// Imports are compared to durable canonical packets in PostgreSQL.
		// Replaying normalized timestamps through the Node raw-envelope
		// equality check would reject equivalent accepted captures.
		{"workflow-values.jsonl", "operational_value_evidence", nil},
		{"workflow-values-collections.jsonl", "workflow_value_collection", inputs.Collections},
	} {
		var buffer bytes.Buffer
		for _, row := range input.rows {
			// Stable canonical parent links are transport annotations, not
			// members of the original checked-in evidence/health envelope.
			record := make(model.Row, len(row))
			for key, value := range row {
				if key != "repositoryId" && key != "workflowId" && key != "provenance" {
					record[key] = value
				}
			}
			if err := json.NewEncoder(&buffer).Encode(map[string]any{
				"schema_version": 2, "kind": input.kind, input.kind: record,
			}); err != nil {
				return err
			}
			if buffer.Len() > 16<<20 {
				return errors.New("native Value transport exceeds its byte budget")
			}
		}
		if err := os.WriteFile(filepath.Join(directory, input.name), buffer.Bytes(), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func readNativeValueEnvelopes(ctx context.Context, filename, kind string) ([]model.Row, error) {
	content, err := readLocalValueFile(ctx, filename, 16<<20)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 4096), 16<<20)
	rows := []model.Row{}
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.UseNumber()
		var envelope map[string]any
		if err := decoder.Decode(&envelope); err != nil {
			return nil, err
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, errors.New("native Value transport contains trailing JSON")
		}
		record, ok := envelope[kind].(map[string]any)
		if !ok || len(envelope) != 3 || envelope["schema_version"] != json.Number("2") || envelope["kind"] != kind {
			return nil, errors.New("invalid native Value transport envelope")
		}
		repository := strings.ToLower(textValue(record["repository"]))
		record["repositoryId"] = "repository:" + model.EncodeCoordinate(repository)
		record["workflowId"] = "workflow:" + model.EncodeCoordinate(repository+":"+textValue(record["sourcePath"]))
		rows = append(rows, model.Row(record))
		if len(rows) > 10000 {
			return nil, errors.New("native Value transport exceeds its row budget")
		}
	}
	return rows, scanner.Err()
}
