package hosting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRejectsProcessOwnedHostPolicy(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(policyPath, []byte(`{
		"version": 1,
		"control-plane": {
			"web": {
				"host": {
					"target": {"module": "container"},
					"operational-store": {"backend": "postgres"}
				}
			}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", policyPath)
	t.Setenv("CAO_POSTGRES_URL", "postgres://127.0.0.1/example?sslmode=disable")
	for _, config := range []Config{
		{},
		{
			SiteDirectory:        t.TempDir(),
			DashboardQueriesPath: "../../dashboard/site/dashboard.json",
			DatabaseQueriesPath:  "../../dashboard/site/src/data/queries/database.json",
		},
	} {
		if _, err := New(t.Context(), config); err == nil || !strings.Contains(err.Error(), "does not delegate listener ownership") {
			t.Fatalf("accepted a process-owned host policy: %v", err)
		}
	}
}
