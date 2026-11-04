package query

import (
	"errors"
	"fmt"
	"strings"
)

type PageKey struct {
	Output string
	Source string
}

type PagePlan struct {
	Output     string
	Boundary   string
	Root       string
	Revision   string
	Comparator string
	Keys       []PageKey
}

// ResolvePagePlan finds the single request-owned page boundary reachable from
// an output and restricts it to a direct, identity-preserving root projection.
// Adapters can therefore push the keyset predicate into the canonical read.
func ResolvePagePlan(definitions []Definition, output string) (*PagePlan, error) {
	index := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		index[definition.Name] = definition
	}
	found := map[string]Definition{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("query %q contains a dependency cycle", output)
		}
		definition, derived := index[name]
		if !derived {
			return nil
		}
		visiting[name] = true
		defer delete(visiting, name)
		if definition.Page != nil {
			found[name] = definition
		}
		for _, input := range definitionInputs(definition) {
			if err := visit(input); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(output); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("paginated query %q reaches more than one page boundary", output)
	}
	var boundary Definition
	for _, definition := range found {
		boundary = definition
	}
	if !strings.HasPrefix(boundary.From, "$") || len(boundary.Union) != 0 || len(boundary.Joins) != 0 ||
		boundary.Filter != nil || len(boundary.Compute) != 0 || boundary.Aggregate != nil ||
		boundary.TemporalSeries != nil || len(boundary.Predict) != 0 || len(boundary.Window) != 0 ||
		boundary.Limit != nil || boundary.Facet != nil {
		return nil, fmt.Errorf("page boundary %q must be a direct canonical projection", boundary.Name)
	}
	selected := map[string]string{}
	if len(boundary.Select) == 0 {
		for _, key := range boundary.Page.Keys {
			selected[key] = key
		}
	} else {
		for _, field := range boundary.Select {
			outputField := field.Field
			if field.As != "" {
				outputField = field.As
			}
			selected[outputField] = field.Field
		}
	}
	plan := &PagePlan{
		Output: output, Boundary: boundary.Name, Root: boundary.From,
		Revision: boundary.Page.Revision, Comparator: boundary.Page.Comparator,
	}
	for _, key := range boundary.Page.Keys {
		source, ok := selected[key]
		if !ok {
			return nil, fmt.Errorf("page boundary %q does not project key %q", boundary.Name, key)
		}
		plan.Keys = append(plan.Keys, PageKey{Output: key, Source: source})
	}
	if plan.Root != "$workflows" || len(plan.Keys) != 3 ||
		plan.Keys[0].Source != "repositoryId" || plan.Keys[1].Source != "path" || plan.Keys[2].Source != "id" {
		return nil, errors.New("workflow-subject page boundary must use repositoryId, path, and id")
	}
	return plan, nil
}

func definitionInputs(definition Definition) []string {
	inputs := append([]string{definition.From}, definition.Union...)
	for _, join := range definition.Joins {
		inputs = append(inputs, join.Source)
	}
	return inputs
}
