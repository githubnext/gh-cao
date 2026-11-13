package postgresx

import (
	"slices"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/query"
)

const statisticalSnapshotPrefix = "$frozen:"

// Only statistical dependencies use the cached summary snapshot. Exact
// consumers in the same plan retain their canonical roots and metadata.
func frozenStatisticalDefinitions(definitions []query.Definition) []query.Definition {
	result := slices.Clone(definitions)
	byName := make(map[string]query.Definition, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	cloned := map[string]bool{}
	var freeze func(string) string
	freeze = func(name string) string {
		if name == "" || name == "$statisticalMembership" {
			return name
		}
		if strings.HasPrefix(name, "$") {
			return statisticalSnapshotPrefix + name
		}
		alias := "frozen:" + name
		if cloned[name] {
			return alias
		}
		cloned[name] = true
		definition, exists := byName[name]
		if !exists {
			return alias
		}
		definition.Name = alias
		definition.From = freeze(definition.From)
		definition.Union = slices.Clone(definition.Union)
		for index, source := range definition.Union {
			definition.Union[index] = freeze(source)
		}
		definition.Joins = slices.Clone(definition.Joins)
		for index, join := range definition.Joins {
			definition.Joins[index].Source = freeze(join.Source)
		}
		result = append(result, definition)
		return alias
	}
	for index, definition := range definitions {
		if !slices.Contains([]string{"statistical-runs", "statistical-tools", "statistical-domains", "statistical-skills", "statistical-audits"}, definition.Name) {
			continue
		}
		result[index].From = freeze(definition.From)
		result[index].Joins = slices.Clone(definition.Joins)
		for joinIndex, join := range definition.Joins {
			result[index].Joins[joinIndex].Source = freeze(join.Source)
		}
	}
	return result
}
