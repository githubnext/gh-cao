package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/dashboard"
)

type nativeCatalog struct {
	dashboard map[string]any
	queries   []map[string]any
	index     map[string]map[string]any
	database  map[string]map[string]any
	pages     []map[string]any
	views     map[string]map[string]any
}

func nativeObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func nativeArray(value any) []any {
	array, _ := value.([]any)
	return array
}

func nativeObjects(value any) []map[string]any {
	result := []map[string]any{}
	for _, entry := range nativeArray(value) {
		if object := nativeObject(entry); object != nil {
			result = append(result, object)
		}
	}
	return result
}

func nativeText(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func nativeUnique(values []string) []string {
	result := []string{}
	for _, value := range values {
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func nativeInputs(definition map[string]any) []string {
	result := []string{nativeText(definition["from"])}
	for _, value := range nativeArray(definition["union"]) {
		result = append(result, nativeText(value))
	}
	for _, join := range nativeObjects(definition["joins"]) {
		result = append(result, nativeText(join["source"]))
	}
	return nativeUnique(result)
}

func nativeReadJSON(files fs.FS, name string) (map[string]any, error) {
	data, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil || document == nil {
		return nil, fmt.Errorf("%s is not a JSON object", name)
	}
	return document, nil
}

func loadNativeCatalog(source string) (*nativeCatalog, error) {
	var files fs.FS = dashboard.Catalog
	name := "site/dashboard.json"
	var root string
	if source != "" {
		absolute, err := filepath.EvalSymlinks(source)
		if err != nil {
			return nil, err
		}
		root = filepath.Dir(absolute)
		files, name = os.DirFS(root), filepath.Base(absolute)
	}
	document, err := nativeReadJSON(files, name)
	if err != nil {
		return nil, err
	}
	body := nativeObject(document["dashboard"])
	if body == nil || nativeText(document["language-version"]) == "" {
		return nil, fmt.Errorf("%s is not a dashboard document", name)
	}
	if fragments, exists := document["fragments"]; exists {
		if _, ok := fragments.([]any); !ok {
			return nil, fmt.Errorf("dashboard fragments must be relative path strings")
		}
	}
	seen := map[string]bool{}
	for _, fragment := range nativeArray(document["fragments"]) {
		relative := nativeText(fragment)
		if relative == "" || !fs.ValidPath(relative) || strings.Contains(relative, `\`) {
			return nil, fmt.Errorf("dashboard fragment path must be relative and remain within its source directory")
		}
		candidate := path.Join(path.Dir(name), relative)
		if root != "" {
			resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(candidate)))
			if err != nil {
				return nil, err
			}
			relative, err := filepath.Rel(root, resolved)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("dashboard fragment path escapes its source directory")
			}
			candidate = filepath.ToSlash(relative)
		}
		if seen[candidate] {
			return nil, fmt.Errorf("duplicate dashboard fragment: %s", candidate)
		}
		seen[candidate] = true
		part, err := nativeReadJSON(files, candidate)
		if err != nil {
			return nil, err
		}
		if _, exists := part["fragments"]; exists {
			return nil, fmt.Errorf("dashboard fragments cannot declare nested fragments")
		}
		for key, value := range part {
			if incoming, ok := value.([]any); ok {
				previous, exists := body[key]
				target, ok := previous.([]any)
				if exists && !ok {
					return nil, fmt.Errorf("cannot append dashboard.%s to a non-array value", key)
				}
				if key == "navigation" {
					for _, group := range incoming {
						added := false
						for _, old := range target {
							left, right := nativeObject(old), nativeObject(group)
							if left != nil && right != nil && left["label"] == right["label"] {
								left["pages"] = append(nativeArray(left["pages"]), nativeArray(right["pages"])...)
								added = true
								break
							}
						}
						if !added {
							target = append(target, group)
						}
					}
				} else {
					target = append(target, incoming...)
				}
				body[key] = target
			} else {
				if _, exists := body[key]; exists {
					return nil, fmt.Errorf("fragment duplicates dashboard.%s", key)
				}
				body[key] = value
			}
		}
	}
	if len(nativeObjects(body["pages"])) == 0 {
		return nil, fmt.Errorf("%s is not a dashboard document", name)
	}
	catalog := &nativeCatalog{
		dashboard: body, queries: nativeObjects(body["queries"]),
		index: map[string]map[string]any{}, database: map[string]map[string]any{}, views: map[string]map[string]any{},
		pages: []map[string]any{},
	}
	for _, query := range catalog.queries {
		if name := nativeText(query["name"]); name != "" {
			catalog.index[name] = query
		}
	}
	definitions, err := dashboard.Catalog.ReadFile("site/src/data/queries/database.json")
	if err != nil {
		return nil, err
	}
	var database []map[string]any
	if err := json.Unmarshal(definitions, &database); err != nil {
		return nil, err
	}
	for _, query := range database {
		catalog.database[nativeText(query["name"])] = query
	}
	for _, view := range nativeObjects(body["views"]) {
		catalog.views[nativeText(view["id"])] = view
	}
	navigation := []string{}
	for _, group := range nativeObjects(body["navigation"]) {
		for _, value := range nativeArray(group["pages"]) {
			navigation = append(navigation, nativeText(value))
		}
	}
	pageIDs := map[string]bool{}
	for _, page := range nativeObjects(body["pages"]) {
		id := nativeText(page["id"])
		route := nativeObject(page["route"])
		if id == "" || nativeText(page["title"]) == "" || pageIDs[id] ||
			(!slices.Contains(navigation, id) && nativeText(route["hash-query-parameter"]) == "") {
			continue
		}
		pageIDs[id] = true
		catalog.pages = append(catalog.pages, page)
	}
	return catalog, nil
}

func nativePageBody(page map[string]any) map[string]any {
	if page["kind"] == "built-in" && nativeObject(page["definition"]) != nil {
		return nativeObject(page["definition"])
	}
	return page
}

func (c *nativeCatalog) pageViews(page map[string]any) []map[string]any {
	result := []map[string]any{}
	for _, entry := range nativeArray(nativePageBody(page)["views"]) {
		view := nativeObject(entry)
		if id := nativeText(entry); id != "" {
			view = c.views[id]
		}
		if view != nil {
			result = append(result, view)
		}
	}
	return result
}

func nativeViewQueries(view map[string]any) []string {
	data := nativeObject(view["data"])
	names := []string{nativeText(data["source"])}
	for _, source := range nativeArray(data["sources"]) {
		names = append(names, nativeText(source))
	}
	drill := nativeObject(nativeObject(view["list"])["drill"])
	return nativeUnique(append(names, nativeText(drill["query"])))
}

func (c *nativeCatalog) pageQueries(page map[string]any) []string {
	names := []string{}
	for _, view := range c.pageViews(page) {
		names = append(names, nativeViewQueries(view)...)
	}
	for _, section := range nativeObjects(nativePageBody(page)["sections"]) {
		names = append(names, nativeText(section["count-source"]))
		for _, source := range nativeArray(section["count-sources"]) {
			names = append(names, nativeText(source))
		}
	}
	names = nativeUnique(names)
	names = slices.DeleteFunc(names, func(name string) bool { return c.index[name] == nil })
	slices.Sort(names)
	return names
}

func nativePageParameters(page map[string]any) []map[string]any {
	result := []map[string]any{}
	seen := map[string]bool{}
	if name := nativeText(nativeObject(page["route"])["hash-query-parameter"]); name != "" {
		description := "The " + name + " this page reports on."
		result = append(result, map[string]any{"name": name, "required": true, "description": description,
			"schema": map[string]any{"type": "string", "description": description}})
		seen[name] = true
	}
	for _, field := range nativeObjects(nativeObject(page["form"])["fields"]) {
		name := nativeText(field["id"])
		if name == "" || seen[name] {
			continue
		}
		description := nativeText(field["label"])
		if description == "" {
			description = name
		}
		schema := map[string]any{"description": description}
		switch field["control"] {
		case "checkbox":
			schema["type"] = "boolean"
			if value, ok := field["default"].(bool); ok {
				schema["default"] = value
			}
		case "text":
			schema["type"] = "string"
			if value, ok := field["default"].(string); ok {
				schema["default"] = value
			}
		case "slider":
			schema["type"] = "number"
			for _, bound := range [][2]string{{"min", "minimum"}, {"max", "maximum"}, {"default", "default"}} {
				if value, ok := field[bound[0]].(float64); ok {
					schema[bound[1]] = value
				}
			}
			if step, ok := field["step"].(float64); ok && step > 0 {
				minimum, _ := field["min"].(float64)
				if math.Abs(minimum/step-math.Round(minimum/step)) < 1e-9 {
					schema["multipleOf"] = step
				}
			}
		case "radio", "select":
			values := []any{}
			for _, option := range nativeArray(field["options"]) {
				if object := nativeObject(option); object != nil {
					option = object["value"]
				}
				switch option.(type) {
				case string, bool, float64:
					values = append(values, option)
				}
			}
			if len(values) == 0 {
				continue
			}
			kind := fmt.Sprintf("%T", values[0])
			mixed := false
			for _, value := range values {
				mixed = mixed || fmt.Sprintf("%T", value) != kind
			}
			if mixed {
				continue
			}
			switch values[0].(type) {
			case string:
				schema["type"] = "string"
			case bool:
				schema["type"] = "boolean"
			case float64:
				schema["type"] = "number"
			}
			schema["enum"] = values
			if slices.Contains(values, field["default"]) {
				schema["default"] = field["default"]
			}
		default:
			continue
		}
		result = append(result, map[string]any{"name": name, "required": false, "description": description, "schema": schema})
		seen[name] = true
	}
	return result
}

func (c *nativeCatalog) dependencies(id string) []string {
	result := []string{}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if query := c.index[name]; query != nil {
			for _, input := range nativeInputs(query) {
				visit(input)
			}
			result = append(result, name)
		}
	}
	visit(id)
	return result
}

func (c *nativeCatalog) queryParameters(id string) []map[string]any {
	parameters := map[string]map[string]any{}
	for _, page := range c.pages {
		route := nativeText(nativeObject(page["route"])["hash-query-parameter"])
		for _, view := range c.pageViews(page) {
			if !slices.Contains(nativeViewQueries(view), id) {
				continue
			}
			data := nativeObject(view["data"])
			if field := nativeText(data["route-field"]); route != "" && field != "" {
				parameters[route] = map[string]any{"name": route, "field": field}
			}
			for _, argument := range nativeObjects(data["arguments"]) {
				name, field := nativeText(argument["name"]), nativeText(argument["field"])
				if name != "" && field != "" {
					parameters[name] = map[string]any{"name": name, "field": field}
				}
			}
		}
	}
	for _, name := range c.dependencies(id) {
		for _, parameter := range nativeObjects(c.index[name]["parameters"]) {
			name, kind := nativeText(parameter["name"]), nativeText(parameter["type"])
			if name == "" || !slices.Contains([]string{"number", "string", "boolean"}, kind) {
				continue
			}
			value := map[string]any{"name": name, "type": kind, "required": true}
			for _, page := range c.pages {
				matches := false
				for _, view := range c.pageViews(page) {
					for _, source := range nativeViewQueries(view) {
						matches = matches || source == id || slices.Contains(c.dependencies(source), id)
					}
				}
				if !matches {
					continue
				}
				for _, candidate := range nativePageParameters(page) {
					if candidate["name"] == name && value["schema"] == nil {
						value["schema"] = candidate["schema"]
					}
				}
			}
			parameters[name] = value
		}
	}
	names := []string{}
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	result := []map[string]any{}
	for _, name := range names {
		result = append(result, parameters[name])
	}
	return result
}

func (c *nativeCatalog) execution(id string) map[string]any {
	requirements, missing := []string{}, []string{}
	seen := map[string]bool{}
	pending := []string{id}
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if query := c.index[name]; query != nil {
			pending = append(pending, nativeInputs(query)...)
		} else if name == "simulation-days" || len(nativeArray(c.database[name]["stores"])) > 0 ||
			slices.Contains([]string{"audits", "domains", "issues", "tools"}, name) {
			requirements = append(requirements, name)
		} else {
			missing = append(missing, name)
		}
	}
	sort.Strings(requirements)
	sort.Strings(missing)
	execution := map[string]any{"local": len(missing) == 0, "requirements": requirements}
	if len(missing) == 0 {
		execution["backend"] = "postgres"
	} else {
		execution["reason"] = "Requires " + strings.Join(missing, ", ") + ", which the local PostgreSQL projection does not provide."
	}
	return execution
}

func (c *nativeCatalog) describePages() []map[string]any {
	result := []map[string]any{}
	for _, page := range c.pages {
		description := nativeText(page["description"])
		if description == "" {
			description = nativeText(page["intent"])
		}
		entry := map[string]any{"id": nativeText(page["id"]), "title": nativeText(page["title"]),
			"description": description, "experimental": page["experimental"] == true,
			"parameters": nativePageParameters(page), "queries": c.pageQueries(page)}
		for _, key := range []string{"intent", "subject", "objective", "acceptance"} {
			if value := nativeText(page[key]); value != "" {
				entry[key] = value
			}
		}
		result = append(result, entry)
	}
	return result
}

func (c *nativeCatalog) describeQuery(id string) map[string]any {
	query := c.index[id]
	if query == nil {
		return nil
	}
	pages := []string{}
	for _, page := range c.pages {
		if slices.Contains(c.pageQueries(page), id) {
			pages = append(pages, nativeText(page["id"]))
		}
	}
	sources := nativeInputs(query)
	sort.Strings(sources)
	sort.Strings(pages)
	entry := map[string]any{"id": id, "subject": nativeText(query["subject"]), "description": nativeText(query["description"]),
		"parameters": c.queryParameters(id), "sources": sources, "used-by-pages": pages, "execution": c.execution(id)}
	for _, key := range []string{"objective", "acceptance"} {
		if value := nativeText(query[key]); value != "" {
			entry[key] = value
		}
	}
	return entry
}

func nativeCatalogCommand(cmd *cobra.Command, args []string) error {
	catalog, err := loadNativeCatalog(nativeString(cmd, "dashboard"))
	if err != nil {
		return err
	}
	if cmd.Name() == "pages" {
		pages := catalog.describePages()
		if len(args) > 0 {
			pages = slices.DeleteFunc(pages, func(page map[string]any) bool { return page["id"] != args[0] })
			if len(pages) == 0 {
				return nativeErrorf("Unknown dashboard page: %s", args[0])
			}
		}
		if nativeBool(cmd, "json") {
			if len(args) > 0 {
				return printJSON(cmd, map[string]any{"command": "pages", "page": pages[0]})
			}
			return printJSON(cmd, map[string]any{"command": "pages", "pages": pages})
		}
		return nativeFormatPages(cmd, pages)
	}
	queries := []map[string]any{}
	if len(args) > 0 {
		if query := catalog.describeQuery(args[0]); query != nil {
			queries = append(queries, query)
		} else {
			return nativeErrorf("Unknown dashboard query: %s", args[0])
		}
	} else {
		for _, query := range catalog.queries {
			queries = append(queries, catalog.describeQuery(nativeText(query["name"])))
		}
	}
	if nativeBool(cmd, "json") {
		if len(args) > 0 {
			return printJSON(cmd, map[string]any{"command": "query-info", "query": queries[0]})
		}
		return printJSON(cmd, map[string]any{"command": "queries", "queries": queries})
	}
	if len(args) == 0 {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Dashboard queries (%d):\n\n", len(queries)); err != nil {
			return err
		}
	}
	for i, query := range queries {
		if err := nativeFormatQuery(cmd, query); err != nil {
			return err
		}
		if len(args) == 0 && i < len(queries)-1 {
			if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
				return err
			}
		}
	}
	return nil
}

func nativeFormatPages(cmd *cobra.Command, pages []map[string]any) error {
	lines := []string{fmt.Sprintf("Dashboard pages (%d):", len(pages))}
	for _, page := range pages {
		experimental := ""
		if page["experimental"] == true {
			experimental = " (experimental)"
		}
		lines = append(lines, fmt.Sprintf("  %s%s  %s", page["id"], experimental, page["title"]))
		if value := nativeText(page["description"]); value != "" {
			lines = append(lines, "    "+value)
		}
		params := []string{}
		for _, parameter := range page["parameters"].([]map[string]any) {
			name := nativeText(parameter["name"])
			if parameter["required"] == true {
				name += " (required)"
			}
			params = append(params, name)
		}
		if len(params) > 0 {
			lines = append(lines, "    parameters: "+strings.Join(params, ", "))
		}
		if queries := page["queries"].([]string); len(queries) > 0 {
			lines = append(lines, "    queries: "+strings.Join(queries, ", "))
		}
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n"))
	return err
}

func nativeFormatQuery(cmd *cobra.Command, query map[string]any) error {
	execution := nativeObject(query["execution"])
	title := "  " + nativeText(query["id"])
	if execution["local"] != true {
		title += "  (not locally executable)"
	}
	lines := []string{title}
	description := nativeText(query["subject"])
	if description == "" {
		description = nativeText(query["description"])
	}
	if description != "" {
		lines = append(lines, "    "+description)
	}
	for _, key := range []string{"objective", "acceptance"} {
		if value := nativeText(query[key]); value != "" {
			lines = append(lines, "    "+key+": "+value)
		}
	}
	parameters := []string{}
	for _, parameter := range query["parameters"].([]map[string]any) {
		parameters = append(parameters, nativeText(parameter["name"]))
	}
	if len(parameters) > 0 {
		lines = append(lines, "    parameters: "+strings.Join(parameters, ", "))
	}
	if pages := query["used-by-pages"].([]string); len(pages) > 0 {
		lines = append(lines, "    pages: "+strings.Join(pages, ", "))
	}
	if execution["local"] == true {
		lines = append(lines, "    postgres collections: "+strings.Join(execution["requirements"].([]string), ", "))
	} else {
		lines = append(lines, "    "+nativeText(execution["reason"]))
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n"))
	return err
}
