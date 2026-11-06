package collect

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestNativeRemoteAttemptHealthSurvivesLocalPreparation(t *testing.T) {
	p, _ := nativeLocalValueFixture(t)
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	attempted := time.Now().UTC().Truncate(time.Millisecond)
	if err := p.RecordNativeValueAttempt(t.Context(), "octo/example", "verify-bugs", attempted,
		"failed", "collector-failed"); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		t.Helper()
		result, _, err := p.Data.ExecuteSQLPlan(t.Context(), []query.Definition{
			{Name: "health", From: "$workflowValueCollections"},
		}, []string{"health"})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range result["health"].Rows {
			if row["repository"] == "octo/example" && row["workflow"] == "verify-bugs" {
				return row
			}
		}
		t.Fatal("reviewed collection health missing")
		return nil
	}
	first := read()
	if first["state"] != "failed" || first["reasonCode"] != "collector-failed" ||
		first["lastAttemptOutcome"] != "failed" || first["lastAttemptAt"] == nil {
		t.Fatalf("real remote attempt was not recorded: %+v", first)
	}
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	later := read()
	if later["state"] != "failed" || later["reasonCode"] != "collector-failed" ||
		later["lastAttemptAt"] != first["lastAttemptAt"] {
		t.Fatalf("local preparation concealed actual collector attempt: %+v", later)
	}
}
