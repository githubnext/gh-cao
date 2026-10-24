package dashboardquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestStatisticalMaximumCandidateFrame(t *testing.T) {
	rows := make([]map[string]any, StatisticalMaxInputRows)
	for index := range rows {
		rows[index] = map[string]any{
			"id":     "github:run:o/r:" + strconv.Itoa(index*99991%200003),
			"status": "completed", "conclusion": "success", "startedAt": "2026-10-22T00:00:00Z",
		}
	}
	for _, capacity := range []int{StatisticalSQLiteCapacity, StatisticalIndexedDBCapacity, StatisticalPostgresCapacity} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			selected, frame, err := SampleStatisticalRuns(rows, StatisticalOptions{
				Context: t.Context(), Watermark: "2026-10-23T00:00:00Z", Capacity: capacity, Complete: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(selected) != capacity || frame["population-count"] != StatisticalMaxInputRows || frame["full-population-estimable"] != true {
				t.Fatalf("maximum frame selection failed: %v", frame)
			}
			for _, row := range selected {
				if row["statistical-frame-id"] != frame["frame-id"] || row["statistical-weight"] != float64(len(rows))/float64(capacity) {
					t.Fatal("selected row lost its population-bound weight")
				}
			}
		})
	}
}

func TestStatisticalDiagnosticsAndBudgets(t *testing.T) {
	rows, options, _ := statisticalFixture(t)
	var messages []string
	options.Seed = "private-seed"
	rows[0]["private-payload"] = "private-content"
	options.Logf = func(format string, args ...any) { messages = append(messages, fmt.Sprintf(format, args...)) }
	if _, _, err := SampleStatisticalRuns(rows, options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(messages[0], "status=completed stage=complete") || !strings.Contains(messages[0], "selected_count=3") ||
		!strings.Contains(messages[0], "weight_valid=true") {
		t.Fatalf("missing sampling completion metrics: %s", messages[0])
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	options.Context = ctx
	if selected, frame, err := SampleStatisticalRuns(rows, options); !errors.Is(err, context.Canceled) || selected != nil || frame != nil {
		t.Fatalf("cancellation exposed partial sampling data: %v", err)
	}
	if !strings.Contains(messages[1], "status=cancelled stage=candidates") {
		t.Fatalf("missing cancellation diagnostics: %s", messages[1])
	}
	options.Context = t.Context()
	options.Watermark = "private-invalid-clock"
	if _, _, err := SampleStatisticalRuns(rows, options); err == nil {
		t.Fatal("invalid clock accepted")
	}
	for _, message := range messages {
		if strings.Contains(message, "private") || strings.Contains(message, "github:") || strings.Contains(message, "sha256:") {
			t.Fatalf("sensitive input escaped into diagnostics: %s", message)
		}
	}
	options.Watermark = "2026-10-23T00:00:00Z"
	invalid := make([]map[string]any, 257)
	payload := strings.Repeat("x", 1<<20)
	for index := range invalid {
		invalid[index] = map[string]any{"id": "invalid", "payload": payload}
	}
	if selected, frame, err := SampleStatisticalRuns(invalid, options); !errors.Is(err, ErrStatisticalBudget) || selected != nil || frame != nil {
		t.Fatalf("invalid identities bypassed byte budget: %v", err)
	}
}

func statisticalFixture(t *testing.T) ([]map[string]any, StatisticalOptions, []string) {
	t.Helper()
	content, err := os.ReadFile("../../dashboard/site/test/fixtures/statistical-sampling.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Rows         []map[string]any
		Watermark    string
		Capacity     int
		ExpectedIDs  []string
		Priority     string
		FrameID      string
		FrameVersion string
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	if StatisticalPriority("github:run:o/r:1", StatisticalSeed) != fixture.Priority {
		t.Fatal("framed SHA256 priority differs from the shared golden fixture")
	}
	options := StatisticalOptions{Watermark: fixture.Watermark, Capacity: fixture.Capacity, Complete: true}
	_, frame, err := SampleStatisticalRuns(fixture.Rows, options)
	if err != nil || frame["frame-id"] != fixture.FrameID || frame["frame-version"] != fixture.FrameVersion {
		t.Fatalf("frame identity differs from the shared golden fixture: %v %v", frame, err)
	}
	return fixture.Rows, options, fixture.ExpectedIDs
}

func TestStatisticalJSParity(t *testing.T) {
	rows, options, _ := statisticalFixture(t)
	t.Run("golden", func(t *testing.T) {
		statisticalJSParity(t, rows, options, []int{0, 1, 3, 20})
	})
	t.Run("bounded-selection", func(t *testing.T) {
		candidates := make([]map[string]any, 513)
		for index := range candidates {
			row := map[string]any{}
			for field, value := range rows[index%6] {
				row[field] = value
			}
			row["id"] = "github:run:o/r:" + strconv.Itoa(index+1)
			row["githubRunId"] = strconv.Itoa(index + 1)
			candidates[index] = row
		}
		statisticalJSParity(t, candidates, options, []int{0, 1, 7, 31, 513, 600})
		slices.Reverse(candidates)
		statisticalJSParity(t, candidates, options, []int{7, 31})
	})
	t.Run("population-identity-order", func(t *testing.T) {
		candidates := []map[string]any{}
		for _, id := range []string{"github:run:o/r:\ue000", "github:run:o/r:😀", "github:run:o/r:1"} {
			row := map[string]any{}
			for field, value := range rows[0] {
				row[field] = value
			}
			row["id"] = id
			candidates = append(candidates, row)
		}
		statisticalJSParity(t, candidates, options, []int{0, 1, 3})
	})
}

func TestStatisticalFrameCoversUnselectedPopulation(t *testing.T) {
	rows := make([]map[string]any, 30)
	for index := range rows {
		rows[index] = map[string]any{
			"id": "github:run:o/r:" + strconv.Itoa(index), "status": "completed",
			"conclusion": "success", "startedAt": "2026-10-22T00:00:00Z",
		}
	}
	for _, capacity := range []int{0, 1} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			options := StatisticalOptions{Watermark: "2026-10-23T00:00:00Z", Capacity: capacity, Complete: true}
			selected, frame, err := SampleStatisticalRuns(rows, options)
			if err != nil {
				t.Fatal(err)
			}
			changed := slices.Clone(rows)
			changed[0] = map[string]any{
				"id": "github:run:o/r:changed0", "status": "completed",
				"conclusion": "success", "startedAt": "2026-10-22T00:00:00Z",
			}
			again, changedFrame, err := SampleStatisticalRuns(changed, options)
			if err != nil {
				t.Fatal(err)
			}
			if frame["frame-id"] == changedFrame["frame-id"] || frame["frame-version"] != StatisticalFrameVersion ||
				!reflect.DeepEqual(frame["strata"], changedFrame["strata"]) || len(selected) != len(again) {
				t.Fatalf("frame did not distinguish equal-sized populations: %v %v", frame, changedFrame)
			}
			for index, row := range selected {
				if row["id"] != again[index]["id"] || row["id"] == rows[0]["id"] ||
					row["statistical-frame-id"] != frame["frame-id"] || again[index]["statistical-frame-id"] != changedFrame["frame-id"] {
					t.Fatal("selected identity changed or weight lost its frame binding")
				}
			}
			replayed := append(slices.Clone(rows), rows[0])
			slices.Reverse(replayed)
			replayedRows, replayedFrame, err := SampleStatisticalRuns(replayed, options)
			if err != nil || replayedFrame["frame-id"] != frame["frame-id"] || !reflect.DeepEqual(replayedRows, selected) {
				t.Fatal("replay or input order changed population identity")
			}
		})
	}
}

func statisticalJSParity(t *testing.T, rows []map[string]any, options StatisticalOptions, capacities []int) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node.js is required for the existing cross-engine parity tests")
	}
	for _, capacity := range capacities {
		options.Capacity = capacity
		for _, complete := range []bool{false, true} {
			options.Complete = complete
			selected, frame, err := SampleStatisticalRuns(rows, options)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{"rows": rows, "options": map[string]any{
				"watermark": options.Watermark, "capacity": capacity, "complete": complete,
			}})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", `
				import {readFileSync} from 'node:fs';
				import {sampleStatisticalRuns} from '../../dashboard/site/src/data/queries/statistical-sample.js';
				const input = JSON.parse(readFileSync(0, 'utf8'));
				process.stdout.write(JSON.stringify(await sampleStatisticalRuns(input.rows,input.options)));
			`)
			command.Stdin = strings.NewReader(string(payload))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("JS sampling failed: %v %s", err, output)
			}
			var actual, expected any
			encoded, err := json.Marshal(map[string]any{"rows": selected, "frame": frame})
			if err != nil {
				t.Fatal(err)
			}
			if json.Unmarshal(output, &actual) != nil || json.Unmarshal(encoded, &expected) != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatalf("JS/Go parity failed for capacity=%d complete=%v:\nJS %s\nGo %s", capacity, complete, output, encoded)
			}
		}
	}
}

func TestStatisticalGoldenAndOrder(t *testing.T) {
	rows, options, expected := statisticalFixture(t)
	result, frame, err := SampleStatisticalRuns(rows, options)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, row := range result {
		ids = append(ids, row["id"].(string))
	}
	if !reflect.DeepEqual(ids, expected) || frame["population-count"] != 6 || frame["weight-valid"] != true {
		t.Fatalf("golden mismatch: ids=%v frame=%v", ids, frame)
	}
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)
	again, againFrame, err := SampleStatisticalRuns(reversed, options)
	if err != nil || !reflect.DeepEqual(result, again) || !reflect.DeepEqual(frame, againFrame) {
		t.Fatal("arrival order changed frozen frame")
	}
	for index, weight := range []float64{3, 1, 2} {
		if result[index]["statistical-weight"] != weight {
			t.Fatal("incorrect inclusion weight")
		}
	}
	duplicates := append(slices.Clone(rows), rows[0])
	again, duplicateFrame, err := SampleStatisticalRuns(duplicates, options)
	if err != nil || !reflect.DeepEqual(result, again) || duplicateFrame["population-count"] != 6 {
		t.Fatal("identical replay changed sample")
	}
}

func TestStatisticalInvalidFramesAndExpiry(t *testing.T) {
	rows, options, _ := statisticalFixture(t)
	for _, timestamp := range []any{nil, "invalid", "2026-02-30T00:00:00Z"} {
		invalid := map[string]any{}
		for field, value := range rows[0] {
			invalid[field] = value
		}
		invalid["startedAt"] = timestamp
		result, frame, err := SampleStatisticalRuns(append(slices.Clone(rows), invalid), options)
		if err != nil || frame["weight-valid"] != false {
			t.Fatalf("invalid frame accepted: %v %v", frame, err)
		}
		for _, row := range result {
			if _, present := row["statistical-weight"]; present {
				t.Fatal("invalid frame advertised weights")
			}
		}
	}
	options.Capacity = 0
	result, frame, err := SampleStatisticalRuns(rows, options)
	if err != nil || len(result) != 0 || frame["population-count"] != 6 || frame["full-population-estimable"] != false {
		t.Fatal("zero-capacity frame is not explicit")
	}
	options.Capacity, options.Watermark = 20, "2026-10-08T12:00:00.001Z"
	result, _, err = SampleStatisticalRuns(rows, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range result {
		if row["id"] == rows[0]["id"] {
			t.Fatal("expired boundary run retained")
		}
	}
	options.HotIDs = map[string]bool{rows[5]["id"].(string): true}
	_, frame, err = SampleStatisticalRuns(rows, options)
	if err != nil || frame["hot-excluded-count"] != 1 {
		t.Fatal("explicit hot exclusion not applied")
	}
	options.Watermark = ""
	if _, _, err := SampleStatisticalRuns(rows, options); err == nil {
		t.Fatal("missing logical watermark accepted")
	}
}

func TestStatisticalDuplicateConflictsAreOrderIndependent(t *testing.T) {
	rows, options, _ := statisticalFixture(t)
	conflict := map[string]any{}
	for field, value := range rows[0] {
		conflict[field] = value
	}
	conflict["conclusion"] = "failure"
	input := append(slices.Clone(rows), conflict, conflict)
	left, leftFrame, err := SampleStatisticalRuns(input, options)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(input)
	right, rightFrame, err := SampleStatisticalRuns(input, options)
	if err != nil || !reflect.DeepEqual(left, right) || !reflect.DeepEqual(leftFrame, rightFrame) ||
		leftFrame["duplicate-conflicts"] != 1 || leftFrame["quarantined-count"] != 1 {
		t.Fatalf("conflicting duplicate order changed frame: %v %v %v", leftFrame, rightFrame, err)
	}
	for _, row := range left {
		if row["id"] == rows[0]["id"] {
			t.Fatal("conflicting canonical identity was not quarantined")
		}
	}
	for _, row := range rows {
		row["statistical-weight"], row["statistical-probability"] = 999, 1
	}

	options.Complete = false
	selected, _, err := SampleStatisticalRuns(rows, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range selected {
		if _, present := row["statistical-weight"]; present {
			t.Fatal("upstream evidence injected an unjustified statistical weight")
		}
	}
}

func TestStatisticalCreatedFallbackFrameIdentityAndBudgets(t *testing.T) {
	rows, options, _ := statisticalFixture(t)
	fallback := map[string]any{}
	for field, value := range rows[0] {
		fallback[field] = value
	}
	fallback["startedAt"], fallback["createdAt"], fallback["observedAt"] = nil, rows[0]["startedAt"], "2099-01-01T00:00:00Z"
	result, frame, err := SampleStatisticalRuns([]map[string]any{fallback}, options)
	if err != nil || len(result) != 1 || !strings.HasPrefix(frame["frame-id"].(string), "sha256:") {
		t.Fatalf("execution fallback or frame identity is invalid: %v %v", frame, err)
	}
	_, again, err := SampleStatisticalRuns([]map[string]any{fallback}, options)
	if err != nil || frame["frame-id"] != again["frame-id"] {
		t.Fatal("frozen frame identity is not deterministic")
	}
	fallback["createdAt"] = nil
	result, frame, err = SampleStatisticalRuns([]map[string]any{fallback}, options)
	if err != nil || len(result) != 0 || frame["weight-valid"] != false {
		t.Fatal("observation time was promoted to execution time")
	}
	if _, _, err := SampleStatisticalRuns(make([]map[string]any, StatisticalMaxInputRows+1), options); err == nil {
		t.Fatal("oversized candidate frame accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	options.Context = ctx
	if _, _, err := SampleStatisticalRuns(rows, options); err != context.Canceled {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
