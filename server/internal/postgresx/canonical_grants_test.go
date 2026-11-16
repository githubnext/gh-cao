package postgresx

import (
	"os"
	"strings"
	"testing"
)

func TestCanonicalRuntimeGrantsCoverEveryCanonicalEntityTable(t *testing.T) {
	content, err := os.ReadFile("../../postgres/canonical-grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	grants := string(content)
	for source, table := range entityTables {
		if !table.canonical {
			continue
		}
		if !strings.Contains(grants, `:"schema".`+table.name) {
			t.Errorf("runtime grant missing canonical source %s table %s", source, table.name)
		}
	}
}
