package cli

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNativeStatisticalNamedQuery(t *testing.T) {
	content, err := os.ReadFile("../../dashboard/site/test/fixtures/statistical-sampling.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Rows []map[string]any }
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	file := nativeTestSnapshot(t, map[string][]map[string]any{"runs": fixture.Rows})
	db, err := openNativeSnapshot(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	catalog.index["statistical-contract"] = map[string]any{"name": "statistical-contract", "from": "statistical-runs"}
	result, err := catalog.runQuery(t.Context(), db, "statistical-contract", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	metadata := nativeObject(result["metadata"])
	frame := nativeObject(metadata["statistical-frame"])
	if metadata["availability"] != "available" || frame["population-count"] != 6 || frame["capacity"] != 8000 ||
		frame["watermark-policy"] != "canonical-run-max" || frame["weight-valid"] != false {
		t.Fatalf("native named query lost statistical semantics: %+v", result)
	}
	for _, row := range result["rows"].([]map[string]any) {
		if row["statistical-frame-id"] != frame["frame-id"] {
			t.Fatal("native named query lost the selected run's frame binding")
		}
	}
}
