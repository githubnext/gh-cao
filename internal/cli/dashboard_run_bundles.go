package cli

import (
	"encoding/json"
	"errors"
	"time"
)

func dashboardRunBundles(batch map[string][]map[string]any, observedAt time.Time) ([]byte, error) {
	type collection struct {
		Coverage string           `json:"coverage"`
		Rows     []map[string]any `json:"rows"`
	}
	type bundle struct {
		Run         map[string]any        `json:"run"`
		Collections map[string]collection `json:"collections,omitempty"`
	}
	if len(batch["runs"]) == 0 || len(batch["runs"]) > 16 {
		return nil, errors.New("streamed Run-bundle publication requires 1-16 normalized Runs")
	}
	publication := struct {
		Version    int                         `json:"version"`
		ObservedAt time.Time                   `json:"observedAt"`
		Runs       []bundle                    `json:"runs"`
		Parents    map[string][]map[string]any `json:"parents,omitempty"`
		Ingestion  map[string]any              `json:"ingestion,omitempty"`
	}{Version: 1, ObservedAt: observedAt.UTC().Truncate(time.Microsecond), Parents: map[string][]map[string]any{}}
	for _, row := range batch["runs"] {
		run := bundle{Run: row, Collections: map[string]collection{}}
		for _, source := range []string{"audits", "domains", "tools", "skills", "friction", "issues",
			"experimentAssignments", "graderObservations", "evalObservations"} {
			records := []map[string]any{}
			for _, child := range batch[source] {
				if child["runId"] == row["id"] {
					records = append(records, child)
				}
			}
			if len(records) > 0 {
				// The streaming collector has not proven artifact completeness.
				// A missing observation never authorizes same-attempt deletion.
				run.Collections[source] = collection{Coverage: "partial", Rows: records}
			}
		}
		publication.Runs = append(publication.Runs, run)
	}
	for _, source := range []string{"campaigns", "repositories", "workflows", "experiments", "graders", "evals"} {
		if len(batch[source]) > 0 {
			publication.Parents[source] = batch[source]
		}
	}
	for _, row := range batch["transactions"] {
		if row["id"] == "local-dashboard-ingestion" {
			publication.Ingestion = row
		}
	}
	content, err := json.Marshal(publication)
	if err != nil {
		return nil, err
	}
	if len(content) > 32<<20 {
		return nil, errors.New("streamed Run-bundle publication exceeds the 32 MiB input budget")
	}
	return content, nil
}
