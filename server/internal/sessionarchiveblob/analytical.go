package sessionarchiveblob

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func kqlIdentifier(name string) string { return "[" + literal(name) + "]" }

func kqlScalar(value sessionarchive.Scalar) string {
	if value.Type == "string" {
		return literal(value.String)
	}
	return "long(" + strconv.FormatInt(value.Integer, 10) + ")"
}

func kqlPredicate(p sessionarchive.PlannedPredicate) string {
	field := kqlIdentifier(p.Field.Name)
	if p.Equals != nil {
		return field + "==" + kqlScalar(*p.Equals)
	}
	if p.In != nil {
		var values []string
		for _, value := range p.In {
			values = append(values, kqlScalar(value))
		}
		return field + " in (" + strings.Join(values, ",") + ")"
	}
	present := "isnotnull(" + field + ")"
	if p.Field.Type == "string" {
		present = "isnotempty(" + field + ")"
	}
	parts := []string{present}
	for _, bound := range []struct {
		value    *sessionarchive.Scalar
		operator string
	}{{p.GTE, ">="}, {p.LT, "<"}} {
		if bound.value != nil {
			comparison := field + bound.operator + kqlScalar(*bound.value)
			if p.Field.Type == "string" {
				comparison = "strcmp(" + field + "," + kqlScalar(*bound.value) + ")" + bound.operator + "0"
			}
			parts = append(parts, comparison)
		}
	}
	return "(" + strings.Join(parts, " and ") + ")"
}

func analyticalKQL(plan sessionarchive.Plan, source string) string {
	inputs := slices.Clone(plan.Inputs)
	for _, name := range []string{"TraceId", "Gaps"} {
		if !slices.Contains(inputs, name) {
			inputs = append(inputs, name)
		}
	}
	projected := make([]string, 0, len(inputs))
	predicates := make([]string, 0, len(plan.Predicates))
	groups := make([]string, 0, len(plan.By))
	reductions := make([]string, 0, len(plan.Values))
	selections := make([]string, 0, len(plan.Selections)+len(plan.Ties))
	orders := make([]string, 0, 2*(len(plan.Orders)+len(plan.Ties)))
	emptyKeys := make([]string, 0, len(plan.Orders)+len(plan.Ties))
	packed := make([]string, 0, 2*len(plan.Columns))
	addOrder := func(field sessionarchive.Column, direction string) {
		name := kqlIdentifier(field.Name)
		if field.Type == "string" {
			// Kusto strings cannot be null; sort a typed emptiness column first.
			empty := kqlIdentifier("__archive_empty_" + strconv.Itoa(len(emptyKeys)))
			emptyKeys = append(emptyKeys, empty+"=iff(isempty("+name+"),1,0)")
			orders = append(orders, empty+" asc")
		}
		orders = append(orders, name+" "+direction+" nulls last")
	}
	for _, name := range inputs {
		projected = append(projected, kqlIdentifier(name))
	}
	for _, predicate := range plan.Predicates {
		predicates = append(predicates, kqlPredicate(predicate))
	}
	for _, group := range plan.By {
		groups = append(groups, kqlIdentifier(group.Name))
	}
	for _, value := range plan.Values {
		field := kqlIdentifier(value.Field.Name)
		present := "isnotnull(" + field + ")"
		if value.Field.Type == "string" {
			present = "isnotempty(" + field + ")"
		}
		expression := "countif(" + present + ")"
		if value.Reducer == "distinct-count" {
			// Exact and bounded by the pinned scan-row budget; never dcount().
			expression = "count_distinctif(" + field + "," + present + ")"
		}
		reductions = append(reductions, kqlIdentifier(value.As)+"="+expression)
	}
	for _, selection := range plan.Selections {
		selections = append(selections, kqlIdentifier(selection.As)+"="+kqlIdentifier(selection.Field.Name))
	}
	for _, order := range plan.Orders {
		addOrder(order.Field, order.Direction)
	}
	for i, tie := range plan.Ties {
		hidden := sessionarchive.Column{Name: "__archive_tie_" + strconv.Itoa(i), Type: tie.Type}
		selections = append(selections, kqlIdentifier(hidden.Name)+"="+kqlIdentifier(tie.Name))
		addOrder(hidden, "asc")
	}
	for _, column := range plan.Columns {
		packed = append(packed, literal(column.Name), kqlIdentifier(column.Name))
	}
	kql := "let source=materialize(" + source + "| project " + strings.Join(projected, ",") + ");\nsource\n"
	if len(predicates) > 0 {
		kql += "| where " + strings.Join(predicates, " and ") + "\n"
	}
	if len(reductions) > 0 {
		kql += "| summarize " + strings.Join(reductions, ",")
		if len(groups) > 0 {
			kql += " by " + strings.Join(groups, ",")
		}
		kql += "\n"
	}
	kql += "| project " + strings.Join(selections, ",") + "\n"
	if len(emptyKeys) > 0 {
		kql += "| extend " + strings.Join(emptyKeys, ",") + "\n"
	}
	if len(orders) > 0 {
		kql += "| order by " + strings.Join(orders, ",") + "\n"
	}
	// One result table keeps the QueryService complete-result contract intact.
	// serialize preserves ordered make_list input; the limit+1 probe is bounded.
	return kql + "| take " + strconv.Itoa(plan.Limit+1) +
		"\n| serialize\n| summarize rows=make_list(bag_pack(" + strings.Join(packed, ",") + "),4097)\n" +
		"| extend tracesWithGaps=toscalar(source | where array_length(Gaps)>0 | summarize by TraceId | count)\n" +
		"| project rows,tracesWithGaps"
}

func (a *Archive) ReadAnalytical(ctx context.Context, scope sessionarchive.Scope, repository string, definition sessionarchive.Definition) (uint64, sessionarchive.AnalyticalResult, *Reader, error) {
	return a.readAnalyticalSource(ctx, scope, repository, definition, nil)
}

func (a *Archive) AnalyticalAvailable() bool {
	return a != nil && a.config.Enabled && a.config.AnalyticalAcceptance != "" &&
		a.config.AnalyticalAcceptance == a.config.AnalyticalAcceptanceBinding()
}

func (n *Native) ReadAnalyticalAt(ctx context.Context, scope sessionarchive.Scope, source sessionarchive.Source, definition sessionarchive.Definition) (uint64, sessionarchive.AnalyticalResult, *Reader, error) {
	return n.readAnalyticalSource(ctx, scope, source.Repository, definition, &source)
}

func (a *Archive) readAnalyticalSource(ctx context.Context, scope sessionarchive.Scope, repository string, definition sessionarchive.Definition, source *sessionarchive.Source) (_ uint64, _ sessionarchive.AnalyticalResult, _ *Reader, err error) {
	if !a.AnalyticalAvailable() {
		return 0, sessionarchive.AnalyticalResult{}, nil, ErrConformance
	}
	plan, err := definition.Plan()
	if err != nil {
		return 0, sessionarchive.AnalyticalResult{}, nil, err
	}
	if err := plan.Require(sessionarchive.AnalyticalCapabilities(4096)); err != nil {
		return 0, sessionarchive.AnalyticalResult{}, nil, err
	}
	queryCtx, generation, files, members, reader, err := a.pinRead(ctx, scope, repository, "", 0, source, true)
	if err != nil {
		return generation, sessionarchive.AnalyticalResult{}, nil, err
	}
	defer func() {
		if err != nil {
			_ = reader.Close()
		}
	}()
	if err := a.verifyTable(queryCtx); err != nil {
		return generation, sessionarchive.AnalyticalResult{}, nil, err
	}
	// Empty sources are typed without reading external objects.
	eventSource := "datatable(" + EventSchema() + ")[]\n"
	prefix := ""
	if len(members) > 0 {
		prefix, eventSource = membershipSourceKQL(a.config.Table, files, members)
	}
	table, err := a.query(queryCtx, "/v1/rest/query", prefix+analyticalKQL(plan, eventSource))
	if err != nil {
		return generation, sessionarchive.AnalyticalResult{}, nil, err
	}
	if len(table.Columns) != 2 || table.Columns[0].ColumnName != "rows" ||
		table.Columns[1].ColumnName != "tracesWithGaps" || len(table.Rows) != 1 || len(table.Rows[0]) != 2 {
		return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
	}
	var rows []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(table.Rows[0][0]))
	decoder.UseNumber()
	if decoder.Decode(&rows) != nil || rows == nil || len(rows) > plan.Limit+1 {
		return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
	}
	var gaps int
	if json.Unmarshal(table.Rows[0][1], &gaps) != nil || gaps < 0 || gaps > len(members) {
		return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
	}
	for _, row := range rows {
		if len(row) != len(plan.Columns) {
			return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
		}
		for _, column := range plan.Columns {
			value, ok := row[column.Name]
			if !ok {
				return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
			}
			if column.Type == "string" {
				if _, ok := value.(string); !ok {
					return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
				}
			} else if value != nil {
				n, ok := value.(json.Number)
				if !ok {
					return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
				}
				if _, err := n.Int64(); err != nil {
					return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
				}
			}
		}
	}
	limited := len(rows) > plan.Limit
	if limited && !plan.ExplicitLimit {
		return generation, sessionarchive.AnalyticalResult{}, nil, sessionarchive.ErrLimit
	}
	if limited {
		rows = rows[:plan.Limit]
	}
	if err := reader.Deliver(func() error { return nil }); err != nil {
		return generation, sessionarchive.AnalyticalResult{}, nil, err
	}
	result := sessionarchive.AnalyticalResult{Columns: plan.Columns, Rows: rows,
		Metadata: sessionarchive.QueryMetadata{Limited: limited, UpstreamCoverage: "unknown",
			AdmissionGaps: reader.pinned.admissionGaps, TracesWithGaps: gaps}}
	return generation, result, reader, nil
}
