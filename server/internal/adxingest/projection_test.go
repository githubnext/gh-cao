package adxingest

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

func testSession() sessionexport.Session {
	return sessionexport.Session{Namespace: "test", Revision: 1,
		Run: model.Row{"id": "run", "repositoryId": "repo", "workflowId": "workflow", "attempt": 1,
			"status": "completed", "inputTokens": int64(0), "outputTokens": nil, "title": "DO_NOT_EXPORT"},
		Facts: map[string][]model.Row{
			"$tools": {{"id": "start", "runId": "run", "type": "tool_call", "name": "read",
				"summary": "DO_NOT_EXPORT", "arguments": "DO_NOT_EXPORT"}},
		}, Quality: map[string]model.Metadata{"$runs": {"availability": "available", "completeness": "partial"}}}
}

func TestProjectionAllowlistPresenceAndNativeEvents(t *testing.T) {
	session := testSession()
	projection, err := project(session)
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "DO_NOT_EXPORT") {
		t.Fatal("raw or unapproved evidence escaped")
	}
	run := projection.Tables["SessionFacts"][0]
	fields := run["PresentFields"].([]string)
	if run["InputTokens"] != int64(0) || run["OutputTokens"] != nil ||
		!slices.Contains(fields, "OutputTokens") || slices.Contains(fields, "ReasoningTokens") ||
		!slices.Equal(run["NullFields"].([]string), []string{"OutputTokens"}) {
		t.Fatalf("lost null/zero/absent: %+v", run)
	}

	if run["Completeness"] != "partial" || len(projection.Tables["ToolEvents"]) != 1 ||
		projection.Tables["ToolEvents"][0]["Type"] != "tool_call" || projection.Manifest["UsageObservations"] != "unavailable" {
		t.Fatal("lost coverage or native-event semantics")
	}
	session.Revision = 2
	unchanged, err := project(session)
	if err != nil || projection.ID != unchanged.ID {
		t.Fatal("canonical revision alone changed the content identity")
	}
	if projection.Manifest["RowDigest"] == unchanged.Manifest["RowDigest"] {
		t.Fatal("ready revision did not advance independently")
	}
	session.Run["inputTokens"] = 5
	corrected, err := project(session)
	if err != nil || corrected.ID == projection.ID {
		t.Fatal("correction did not change identity")
	}
}

func TestProjectionDistinguishesNullAndEmptyString(t *testing.T) {
	for _, value := range []any{nil, ""} {
		session := testSession()
		session.Facts["$tools"][0]["name"] = value
		projection, err := project(session)
		if err != nil {
			t.Fatal(err)
		}
		row := projection.Tables["ToolEvents"][0]
		if !slices.Contains(row["PresentFields"].([]string), "Name") ||
			slices.Contains(row["NullFields"].([]string), "Name") != (value == nil) {
			t.Fatal("Kusto string coercion would lose explicit null evidence")
		}
	}
}

func TestProjectionRejectsInvalidAndOversizedEvidence(t *testing.T) {
	for _, mutate := range []func(*sessionexport.Session){
		func(s *sessionexport.Session) { delete(s.Run, "id") },
		func(s *sessionexport.Session) { s.Run["inputTokens"] = "17" },
		func(s *sessionexport.Session) { s.Run["attempt"] = "1" },
		func(s *sessionexport.Session) { s.Facts["$tools"][0]["runId"] = "wrong" },
		func(s *sessionexport.Session) { s.Facts["$tools"] = append(s.Facts["$tools"], s.Facts["$tools"][0]) },
	} {
		session := testSession()
		mutate(&session)
		if _, err := project(session); err == nil {
			t.Fatal("malformed projection accepted")
		}
	}
	session := testSession()
	for i := 0; i < sessionexport.MaxRows; i++ {
		session.Facts["$tools"] = append(session.Facts["$tools"], model.Row{
			"id": strings.Repeat("x", i+1), "runId": "run",
		})
	}
	if _, err := project(session); !errors.Is(err, sessionexport.ErrTooLarge) {
		t.Fatalf("oversized projection: %v", err)
	}
}

func TestSchemaUsesOnlyFreeCompatibleFeatures(t *testing.T) {
	schema := Schema()
	if strings.Count(schema, ".create-merge table ") != 6 ||
		strings.Count(schema, "mapping 'cao_v1'") != 6 ||
		!strings.Contains(schema, "SourceRevision") || !strings.Contains(schema, "softdelete = 7d") {
		t.Fatal("schema does not match projection contract")
	}
	for _, forbidden := range []string{"materialized-view", "external table", "purge", "managed_identity", "EventGrid"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("paid dependency: %s", forbidden)
		}
	}
}

func TestObservationRowRecordsPresenceAndQuality(t *testing.T) {
	session := testSession()
	var runFamily family
	for _, candidate := range families {
		if candidate.source == "$runs" {
			runFamily = candidate
		}
	}
	row, err := observationRow(session, "run", runFamily, "run", session.Run)
	if err != nil {
		t.Fatal(err)
	}
	if row["ObservationId"] != "run" || row["RunAttempt"] != 1 || row["Availability"] != "available" ||
		row["Completeness"] != "partial" || row["Freshness"] != "unknown" {
		t.Fatalf("unexpected row: %+v", row)
	}
	if _, leaked := row["Title"]; leaked {
		t.Fatal("unapproved field escaped")
	}
	if !slices.Equal(row["NullFields"].([]string), []string{"OutputTokens"}) {
		t.Fatalf("null fields = %v", row["NullFields"])
	}
}

func TestObservationRowRejectsInvalidValues(t *testing.T) {
	session := testSession()
	var runFamily family
	for _, candidate := range families {
		if candidate.source == "$runs" {
			runFamily = candidate
		}
	}
	bad := model.Row{"id": "run", "status": 7}
	if _, err := observationRow(session, "run", runFamily, "run", bad); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("expected invalid status error, got %v", err)
	}
	session.Run["attempt"] = "one"
	if _, err := observationRow(session, "run", runFamily, "run", model.Row{"id": "run"}); err == nil {
		t.Fatal("expected invalid attempt error")
	}
}
