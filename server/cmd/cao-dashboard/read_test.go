package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestCanonicalReadDefinition(t *testing.T) {
	one := 1
	tests := []struct {
		name    string
		request dashboardquery.CanonicalReadRequest
		want    query.Definition
	}{
		{
			name:    "collection with fields",
			request: dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "runs", Fields: []string{"id", "status"}},
			want: query.Definition{Name: "native-read", From: "$runs",
				Select: []query.SelectedField{{Field: "id"}, {Field: "status"}}},
		},
		{
			name:    "transactions are readable",
			request: dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "transactions"},
			want:    query.Definition{Name: "native-read", From: "$transactions"},
		},
		{
			name:    "record ids become an id predicate",
			request: dashboardquery.CanonicalReadRequest{Kind: "measurements", Collection: "runs", RecordIDs: []string{"a", "b"}},
			want: query.Definition{Name: "native-read", From: "$runs",
				Filter: &query.Filter{Predicates: []query.Predicate{{Field: "id", In: []any{"a", "b"}}}}},
		},
		{
			name: "evidence facts read the evidence source with selections",
			request: dashboardquery.CanonicalReadRequest{Kind: "evidence-facts",
				Selections: []dashboardquery.EvidenceSelection{{Field: "report-id", Value: "r1"}, {Field: "workflow-route", Value: "w1"}}},
			want: query.Definition{Name: "native-read", From: "$operationalValueEvidence",
				Filter: &query.Filter{Predicates: []query.Predicate{
					{Field: "report-id", Equals: "r1"}, {Field: "workflow-route", Equals: "w1"}}}},
		},
		{
			name:    "daily run count metadata is limited to one row",
			request: dashboardquery.CanonicalReadRequest{Kind: "daily-run-count-metadata", MaxRows: 5, MaxBytes: 1 << 30},
			want:    query.Definition{Name: "native-read", From: "daily-run-counts", Limit: &one},
		},
		{
			name:    "daily run count records",
			request: dashboardquery.CanonicalReadRequest{Kind: "daily-run-count-records"},
			want:    query.Definition{Name: "native-read", From: "$dailyRunCounts"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := canonicalReadDefinition(test.request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("definition = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCanonicalReadDefinitionRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name    string
		request dashboardquery.CanonicalReadRequest
		want    string
	}{
		{"unknown collection", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "nope"}, "unknown canonical collection: nope"},
		{"raw evidence packets", dashboardquery.CanonicalReadRequest{Kind: "evidence", Collection: "operationalValueEvidence"}, "raw evidence packet reads are unsupported"},
		{"unknown kind", dashboardquery.CanonicalReadRequest{Kind: "bogus"}, "unsupported canonical read kind"},
		{"negative row budget", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "runs", MaxRows: -1}, "invalid canonical read budget"},
		{"row budget too large", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "runs", MaxRows: 500_001}, "invalid canonical read budget"},
		{"negative byte budget", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "runs", MaxBytes: -1}, "invalid canonical read budget"},
		{"byte budget too large", dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "runs", MaxBytes: 1<<30 + 1}, "invalid canonical read budget"},
		{
			"selection not allowed for kind",
			dashboardquery.CanonicalReadRequest{Kind: "collection", Collection: "runs",
				Selections: []dashboardquery.EvidenceSelection{{Field: "id", Value: "x"}}},
			"unsupported canonical selection",
		},
		{
			"selection field not allowed for kind",
			dashboardquery.CanonicalReadRequest{Kind: "evidence-facts",
				Selections: []dashboardquery.EvidenceSelection{{Field: "id", Value: "x"}}},
			"unsupported canonical selection",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := canonicalReadDefinition(test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestReadCanonicalLocalMetadataNeedsNoReader(t *testing.T) {
	got, err := readCanonical(context.Background(), nil, dashboardquery.CanonicalReadRequest{Kind: "local-metadata"})
	if err != nil || got.Available || got.Rows != nil {
		t.Fatalf("local-metadata = %#v, %v; want unavailable result", got, err)
	}
}

func TestReadCanonicalRejectsBeforeReading(t *testing.T) {
	if _, err := readCanonical(context.Background(), nil, dashboardquery.CanonicalReadRequest{Kind: "bogus"}); err == nil ||
		err.Error() != "unsupported canonical read kind" {
		t.Fatalf("error = %v", err)
	}
}
