package hostedcli

import (
	"encoding/json"
	"testing"
)

func TestRepositorySelectorFailsClosedOnInvalidDeclarations(t *testing.T) {
	const declaration = `{"id":"add","command":"gh cao repos add {{repository}}","selector":{
		"query":{"command":"gh search repos","fields":["fullName","description"],"limit":20},
		"mapping":{"value":"fullName","label":"fullName","description":"description","target":"repository"}}}`
	for name, mutate := range map[string]func(*Action){
		"valid":         func(*Action) {},
		"undeclared":    func(a *Action) { a.Selector = nil },
		"copy only":     func(a *Action) { a.CopyOnly = true },
		"command":       func(a *Action) { a.Selector.Query.Command = "gh repo delete" },
		"unbounded":     func(a *Action) { a.Selector.Query.Limit = 51 },
		"zero limit":    func(a *Action) { a.Selector.Query.Limit = 0 },
		"unknown field": func(a *Action) { a.Selector.Query.Fields = []string{"secret"} },
		"duplicate":     func(a *Action) { a.Selector.Query.Fields = []string{"fullName", "fullName"} },
		"mapping":       func(a *Action) { a.Selector.Mapping.Value = "unselected" },
		"target":        func(a *Action) { a.Selector.Mapping.Target = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			var action Action
			if err := json.Unmarshal([]byte(declaration), &action); err != nil {
				t.Fatal(err)
			}

			mutate(&action)
			catalog := &Catalog{actions: map[string]Action{action.ID: action}}
			_, err := catalog.ResolveRepositorySelector("add")
			if (err == nil) != (name == "valid") {
				t.Fatalf("unexpected selector admission: %v", err)
			}
		})
	}
}

func TestRepositorySelectorExcludesUpstreamOperationTarget(t *testing.T) {
	selector := RepositorySelector{}
	selector.Query.Limit = 20
	selector.Mapping.Value, selector.Mapping.Label = "fullName", "fullName"
	options, err := selector.Options([]map[string]any{
		{"fullName": "GitHub/GH-AW"}, {"fullName": "github/gh-aw-actions"},
	})
	if err != nil || len(options) != 1 || options[0].Value != "github/gh-aw-actions" {
		t.Fatalf("upstream exclusion filtered the wrong repository: %+v %v", options, err)
	}
}
