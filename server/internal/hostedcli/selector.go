package hostedcli

import (
	"errors"
	"slices"
)

// RepositorySelector is the catalog-owned, bounded GitHub repository lookup.
type RepositorySelector struct {
	Query struct {
		Command string   `json:"command"`
		Fields  []string `json:"fields"`
		Limit   int      `json:"limit"`
	} `json:"query"`
	Mapping struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
		Target      string `json:"target"`
	} `json:"mapping"`
}

type RepositoryOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ResolveRepositorySelector uses only the declaration; browsers cannot select
// executable text, fields, a mapping or an unbounded result count.
func (c *Catalog) ResolveRepositorySelector(id string) (RepositorySelector, error) {
	action, found := c.actions[id]
	if !found || action.Selector == nil || action.CopyOnly {
		return RepositorySelector{}, requestError(404, "Repository selector is not declared by this dashboard.")
	}
	selector := *action.Selector
	fields := []string{"fullName", "description", "name", "owner", "url", "visibility", "isArchived", "isPrivate"}
	if selector.Query.Command != "gh search repos" || selector.Query.Limit < 1 || selector.Query.Limit > 50 ||
		len(selector.Query.Fields) == 0 || len(selector.Query.Fields) > len(fields) {
		return selector, requestError(400, "Invalid repository selector declaration.")
	}
	seen := map[string]bool{}
	for _, field := range selector.Query.Fields {
		if !slices.Contains(fields, field) || seen[field] {
			return selector, requestError(400, "Invalid repository selector fields.")
		}
		seen[field] = true
	}
	if !seen[selector.Mapping.Value] || !seen[selector.Mapping.Label] ||
		selector.Mapping.Description != "" && !seen[selector.Mapping.Description] ||
		!slices.ContainsFunc(templateToken.FindAllStringSubmatch(action.Command, -1), func(match []string) bool {
			return match[1] == selector.Mapping.Target && !contextFields[match[1]]
		}) {
		return selector, requestError(400, "Invalid repository selector mapping.")
	}
	return selector, nil
}

func (s RepositorySelector) Options(rows []map[string]any) ([]RepositoryOption, error) {
	if len(rows) > s.Query.Limit {
		return nil, errors.New("repository search exceeded the declared limit")
	}
	options := make([]RepositoryOption, 0, len(rows))
	for _, row := range rows {
		value, valueOK := row[s.Mapping.Value].(string)
		label, labelOK := row[s.Mapping.Label].(string)
		description := ""
		if s.Mapping.Description != "" && row[s.Mapping.Description] != nil {
			var ok bool
			description, ok = row[s.Mapping.Description].(string)
			if !ok {
				return nil, errors.New("repository search returned an invalid description")
			}
		}
		if !valueOK || !ValidRepository(value) || !labelOK || label == "" {
			return nil, errors.New("repository search returned an invalid option")
		}
		if !LocalRepositoryOperationsAllowed(value) {
			continue
		}
		options = append(options, RepositoryOption{Value: value, Label: label, Description: description})
	}
	return options, nil
}
