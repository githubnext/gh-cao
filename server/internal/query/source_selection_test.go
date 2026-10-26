package query

import "testing"

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
