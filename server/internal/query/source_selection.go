package query

import "slices"

type SourceEquality struct{ Field, Value string }

// SourceEqualities unions necessary pre-compute equalities across every
// reachable consumer. A shared unscoped consumer disables narrowing.
func SourceEqualities(definitions []Definition, requested []string, source, raw string, fields ...string) []SourceEquality {
	if slices.Contains(requested, source) || slices.Contains(requested, raw) {
		return nil
	}
	order, err := Dependencies(definitions, requested)
	if err != nil {
		return nil
	}
	inputs := func(definition Definition) []string {
		result := append([]string{definition.From}, definition.Union...)
		for _, join := range definition.Joins {
			result = append(result, join.Source)
		}
		return result
	}
	var selections []SourceEquality
	for _, definition := range definitions {
		if !slices.Contains(order, definition.Name) {
			continue
		}
		if definition.Name == source {
			if definition.From != raw || len(definition.Compute) > 0 || len(definition.Union) > 0 ||
				len(definition.Joins) > 0 || definition.Aggregate != nil || len(definition.Window) > 0 ||
				definition.TemporalSeries != nil || definition.Facet != nil || len(definition.Predict) > 0 || definition.Limit != nil {
				return nil
			}
			for _, field := range fields {
				if len(definition.Select) > 0 && !slices.ContainsFunc(definition.Select, func(selected SelectedField) bool {
					return selected.Field == field && (selected.As == "" || selected.As == field)
				}) {
					return nil
				}
			}
		}
		if definition.Name != source && slices.Contains(inputs(definition), raw) {
			return nil
		}
		if !slices.Contains(inputs(definition), source) {
			continue
		}
		if definition.From != source || len(definition.Union) > 0 || len(definition.Joins) > 0 ||
			len(definition.Compute) > 0 || definition.Filter == nil {
			return nil
		}
		found := false
		for _, predicate := range definition.Filter.Predicates {
			value, ok := predicate.Equals.(string)
			if !predicate.Optional && ok && slices.Contains(fields, predicate.Field) {
				selection := SourceEquality{Field: predicate.Field, Value: value}
				if !slices.Contains(selections, selection) {
					selections = append(selections, selection)
				}
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	if len(selections) > MaxPredicateAlternatives {
		return nil
	}
	return selections
}
