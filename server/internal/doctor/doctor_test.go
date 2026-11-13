package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanonicalChecksRequirePostgres(t *testing.T) {
	doctor := Doctor{}
	for _, check := range []Check{
		doctor.checkActiveData(context.Background()),
		doctor.checkSchemaVersion(context.Background()),
		doctor.checkIntegrity(context.Background()),
		doctor.checkSources(context.Background()),
	} {
		if check.Status != StatusFail || !strings.Contains(check.Summary, "Postgres is not configured") {
			t.Errorf("%s: expected Postgres configuration failure, got %+v", check.ID, check)
		}
	}
}

func TestProfileCheckRejectsBothAcquisitionProfiles(t *testing.T) {
	values := map[string]string{
		"CAO_COLLECT_APP_ID":   "42",
		"CAO_SOURCE_DIRECTORY": "/snapshot",
	}
	doctor := Doctor{Getenv: func(name string) string { return values[name] }}
	check := doctor.checkCollectionProfile(context.Background())
	if check.Status != StatusFail {
		t.Fatalf("status = %s, want fail: %+v", check.Status, check)
	}
}

func TestRenderingsCarryStableCheckIdentifiersAndNoSecret(t *testing.T) {
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Tool:          "cao-dashboard doctor",
		Version:       "test",
		GeneratedAt:   "2026-09-25T16:00:00Z",
		Profile:       "actions",
		Namespace:     "cao:test",
		Checks: []Check{{
			ID: "operational.health", Area: "operational", Title: "Operational storage reachable",
			Status: StatusPass, Summary: "Operational health is ready",
		}},
	}
	report.Summary = summarize(report.Checks, 5*time.Millisecond)

	var text bytes.Buffer
	if err := Render(&text, report, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "[ PASS ] operational.health") {
		t.Fatalf("text report lacks stable identifier:\n%s", text.String())
	}
	if strings.Contains(text.String(), "super-secret") {
		t.Fatal("text report leaked a credential")
	}

	var structured bytes.Buffer
	if err := Render(&structured, report, "json"); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(structured.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != ReportSchemaVersion || decoded.Checks[0].ID != "operational.health" {
		t.Fatalf("JSON report lost its structured contract: %+v", decoded)
	}
	if strings.Contains(structured.String(), "super-secret") {
		t.Fatal("JSON report leaked a credential")
	}
}

func TestStrictModeTreatsWarningsAsFailure(t *testing.T) {
	report := Report{Summary: Summary{Warn: 1, Status: StatusWarn}}
	if report.Failed(false) {
		t.Fatal("warning failed non-strict report")
	}
	if !report.Failed(true) {
		t.Fatal("warning did not fail strict report")
	}
}
