package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeCatalogMatchesDashboardContract(t *testing.T) {
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	node, repository := nodeRuntime(t)
	program := `
const { readAgentCatalog } = await import(process.argv[1]);
process.stdout.write(JSON.stringify(await readAgentCatalog()));
`
	output, err := exec.Command(node, "--input-type=module", "-e", program, filepath.Join(repository, "dashboard", "report", "agent-catalog.mjs")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var baseline struct {
		Pages   []map[string]any `json:"pages"`
		Queries []map[string]any `json:"queries"`
	}
	if err := json.Unmarshal(output, &baseline); err != nil {
		t.Fatal(err)
	}
	normalize := func(value any) any {
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var normalized any
		if err := json.Unmarshal(content, &normalized); err != nil {
			t.Fatal(err)
		}
		return normalized
	}
	if got, want := normalize(catalog.describePages()), normalize(baseline.Pages); !reflect.DeepEqual(got, want) {
		for i, page := range catalog.describePages() {
			if i >= len(baseline.Pages) || !reflect.DeepEqual(normalize(page), normalize(baseline.Pages[i])) {
				t.Errorf("page %s differs:\ngot  %#v\nwant %#v", page["id"], normalize(page), normalize(baseline.Pages[i]))
			}
		}
		t.Fatalf("native pages differ from production catalog (%d vs %d)", len(catalog.pages), len(baseline.Pages))
	}
	if len(catalog.queries) != len(baseline.Queries) {
		t.Fatalf("query count %d != %d", len(catalog.queries), len(baseline.Queries))
	}
	for _, query := range baseline.Queries {
		id := nativeText(query["id"])
		if got, want := normalize(catalog.describeQuery(id)), normalize(query); !reflect.DeepEqual(got, want) {
			t.Errorf("query %s differs:\ngot  %#v\nwant %#v", id, got, want)
		}
	}
}

func TestNativeCatalogDefaultIsEmbedded(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CAO_RUNTIME_ROOT", "/does/not/exist")
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.pages) == 0 || len(catalog.queries) == 0 || catalog.describeQuery("campaign-runs") == nil {
		t.Fatal("standalone catalog did not contain the reviewed dashboard definitions")
	}
}

func TestNativeCatalogRejectsEscapingFragments(t *testing.T) {
	directory := t.TempDir()
	for _, fragment := range []string{"../escape.json", "/absolute.json", `..\escape.json`} {
		path := filepath.Join(directory, "dashboard.json")
		content, err := json.Marshal(map[string]any{"language-version": "0.1.0", "dashboard": map[string]any{"pages": []any{}}, "fragments": []string{fragment}})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, path, string(content))
		if _, err := loadNativeCatalog(path); err == nil {
			t.Errorf("accepted fragment %q", fragment)
		}
	}
}
