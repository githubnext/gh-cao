package query

import "testing"

func TestSnapshotOnlyEvidenceSelection(t *testing.T) {
	for _, value := range []string{"packet|snapshot", "packet|snapshot|opaque-suffix"} {
		definitions := []Definition{
			{Name: "operational-value-evidence", From: "$operationalValueEvidence"},
			{Name: "selected", From: "operational-value-evidence",
				Filter: &Filter{Predicates: []Predicate{{Field: "snapshot-route", Equals: value}}}},
		}
		selected := OperationalValueEvidenceSelections(definitions, []string{"selected"})
		if len(selected) != 1 || selected[0] != (SourceEquality{Field: "report-id", Value: "packet"}) {
			t.Fatalf("snapshot-only route did not select its packet: %+v", selected)
		}
		definitions[1].Filter.Predicates[0].Equals = "unknown"
		if OperationalValueEvidenceSelections(definitions, []string{"selected"}) != nil {
			t.Fatal("optional missing snapshot identities cannot narrow the packet population")
		}
	}
}

func TestSourceEqualitiesRejectsOverriddenIdentityProjection(t *testing.T) {
	limit := 1
	for _, source := range []Definition{
		{Name: "facts", From: "$facts", Limit: &limit},
		{Name: "facts", From: "$different"},
		{Name: "facts", From: "$facts", Compute: []ComputedField{{As: "id", Function: "literal", Args: []Argument{{Value: "changed"}}}}},
		{Name: "facts", From: "$facts", Select: []SelectedField{{Field: "other", As: "id"}}},
	} {
		definitions := []Definition{source, {
			Name: "selected", From: "facts", Filter: &Filter{Predicates: []Predicate{{Field: "id", Equals: "changed"}}},
		}}
		if SourceEqualities(definitions, []string{"selected"}, "facts", "$facts", "id") != nil {
			t.Fatal("a transformed or limited source is not an identity-preserving storage selection")
		}
	}
}
