package cli

import (
	"maps"

	"github.com/githubnext/gh-cao/dashboard"
)

func (c *nativeCatalog) localDashboardQueries() (*nativeCatalog, error) {
	profile, err := nativeReadJSON(dashboard.Catalog, "site/dashboard-fragments/immediate-profile.json")
	if err != nil {
		return nil, err
	}
	local := *c
	local.index, local.database = maps.Clone(c.index), maps.Clone(c.database)
	aliases := nativeObject(profile["query-source-aliases"])
	for name, definition := range c.index {
		merged := maps.Clone(definition)
		if alias := nativeText(aliases[nativeText(definition["from"])]); alias != "" {
			merged["from"] = alias
		}
		if definition["joins"] != nil {
			joins := []any{}
			for _, join := range nativeObjects(definition["joins"]) {
				copied := maps.Clone(join)
				if alias := nativeText(aliases[nativeText(join["source"])]); alias != "" {
					copied["source"] = alias
				}
				joins = append(joins, copied)
			}
			merged["joins"] = joins
		}
		local.index[name] = merged
	}
	return &local, nil
}
