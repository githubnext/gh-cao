package main

import (
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

func TestCanonicalReadDefinitionSources(t *testing.T) {
	tests := []struct {
		name    string
		request dashboardquery.CanonicalReadRequest
		source  string
	}{
		{"collection", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "runs"}, "$runs"},
		{"evidence facts", dashboardquery.CanonicalReadRequest{Kind: "evidence-facts"}, "$operationalValueEvidence"},
		{"daily counts", dashboardquery.CanonicalReadRequest{Kind: "daily-run-counts"}, "daily-run-counts"},
		{"daily records", dashboardquery.CanonicalReadRequest{Kind: "daily-run-count-records"}, "$dailyRunCounts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition, localOnly, err := canonicalReadDefinition(test.request)
			if err != nil || localOnly {
				t.Fatalf("unexpected result: localOnly=%t err=%v", localOnly, err)
			}
			if definition.Name != "native-read" || definition.From != test.source {
				t.Fatalf("definition = %+v, want source %q", definition, test.source)
			}
		})
	}
}

func TestCanonicalReadDefinitionShapesQuery(t *testing.T) {
	definition, _, err := canonicalReadDefinition(dashboardquery.CanonicalReadRequest{
		Kind: "daily-run-count-metadata", Fields: []string{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Limit == nil || *definition.Limit != 1 || len(definition.Select) != 2 || definition.Filter != nil {
		t.Fatalf("definition = %+v", definition)
	}

	definition, _, err = canonicalReadDefinition(dashboardquery.CanonicalReadRequest{
		Kind:       "measurements",
		Collection: "runs",
		RecordIDs:  []string{"one", "two"},
		Selections: []dashboardquery.EvidenceSelection{{Field: "workflow-route", Value: "route"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Filter == nil || len(definition.Filter.Predicates) != 2 {
		t.Fatalf("filter = %+v", definition.Filter)
	}
	if ids := definition.Filter.Predicates[0]; ids.Field != "id" || len(ids.In) != 2 {
		t.Fatalf("id predicate = %+v", ids)
	}
	if route := definition.Filter.Predicates[1]; route.Field != "workflow-route" || route.Equals != "route" {
		t.Fatalf("route predicate = %+v", route)
	}
}

func TestCanonicalReadDefinitionLocalMetadata(t *testing.T) {
	_, localOnly, err := canonicalReadDefinition(dashboardquery.CanonicalReadRequest{Kind: "local-metadata"})
	if err != nil || !localOnly {
		t.Fatalf("localOnly=%t err=%v", localOnly, err)
	}
}

func TestCanonicalReadDefinitionRejections(t *testing.T) {
	tests := []struct {
		name    string
		request dashboardquery.CanonicalReadRequest
		want    string
	}{
		{"unknown kind", dashboardquery.CanonicalReadRequest{Kind: "bogus"}, "unsupported canonical read kind"},
		{"unknown collection", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "nope"}, "unknown canonical collection"},
		{"raw evidence", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "operationalValueEvidence"}, "raw evidence packet reads are unsupported"},
		{"negative rows", dashboardquery.CanonicalReadRequest{Kind: "daily-run-counts", MaxRows: -1}, "invalid canonical read budget"},
		{"too many rows", dashboardquery.CanonicalReadRequest{Kind: "daily-run-counts", MaxRows: 500_001}, "invalid canonical read budget"},
		{"too many bytes", dashboardquery.CanonicalReadRequest{Kind: "daily-run-counts", MaxBytes: 1<<30 + 1}, "invalid canonical read budget"},
		{"unsupported selection", dashboardquery.CanonicalReadRequest{
			Kind: "evidence-facts", Selections: []dashboardquery.EvidenceSelection{{Field: "id", Value: "x"}},
		}, "unsupported canonical selection"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := canonicalReadDefinition(test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}
