package sessionarchiveblob

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func kqlIdentifier(name string) string { return "[" + literal(name) + "]" }

func kqlScalar(value sessionarchive.Scalar) string {
	switch value.Type {
	case "string":
		return literal(value.String)
	case "number":
		return "real(" + strconv.FormatFloat(value.Number, 'g', -1, 64) + ")"
	case "boolean":
		return strconv.FormatBool(value.Boolean)
	default:
		return "long(" + strconv.FormatInt(value.Integer, 10) + ")"
	}
}

func kqlPredicate(p sessionarchive.PlannedPredicate) string {
	field := kqlIdentifier(p.Field.Name)
	if p.State != "" {
		present := "set_has_element(PresentFields," + literal(p.Field.Name) + ")"
		null := "set_has_element(NullFields," + literal(p.Field.Name) + ")"
		invalid := "set_has_element(InvalidFields," + literal(p.Field.Name) + ")"
		omission := func(kind string) string {
			paths := p.OmissionPaths(kind)
			parts := make([]string, 0, len(paths))
			for _, path := range paths {
				parts = append(parts, "set_has_element(OmissionReasons,"+literal(path)+")")
			}
			return "(" + strings.Join(parts, " or ") + ")"
		}
		excluded, redacted := omission("excluded"), omission("redacted")
		switch p.State {
		case "present":
			return present
		case "absent":
			return "(not(" + present + ") and not(" + excluded + ") and not(" + redacted + "))"
		case "null":
			return null
		case "invalid":
			return invalid
		case "excluded":
			return excluded
		case "redacted":
			return redacted
		case "valid":
			return "(" + present + " and not(" + null + ") and not(" + invalid + ") and not(" + excluded + ") and not(" + redacted + "))"
		}
	}
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

func kqlExpression(e sessionarchive.PlannedExpression) string {
	if e.Field != "" {
		return kqlIdentifier(e.Field)
	}
	if e.Function == "" {
		if e.Literal != nil {
			return kqlScalar(*e.Literal)
		}
		return map[string]string{"number": "real(null)", "integer": "long(null)", "boolean": "bool(null)", "string": `""`}[e.Type]
	}
	var args []string
	for _, arg := range e.Args {
		args = append(args, kqlExpression(arg))
	}
	switch e.Function {
	case "and":
		return "(coalesce(" + args[0] + ",false) and coalesce(" + args[1] + ",false))"
	case "if":
		if e.Type == "number" {
			args[1], args[2] = "toreal("+args[1]+")", "toreal("+args[2]+")"
		}
		return "iff(" + strings.Join(args, ",") + ")"
	case "is-null":
		return "isnull(" + args[0] + ")"
	case "equals", "gt", "lte":
		op := map[string]string{"equals": "==", "gt": ">", "lte": "<="}[e.Function]
		return "coalesce((" + args[0] + op + args[1] + "),false)"
	case "coalesce":
		if e.Type == "number" {
			args[0], args[1] = "toreal("+args[0]+")", "toreal("+args[1]+")"
		}
		return "coalesce(" + strings.Join(args, ",") + ")"
	case "sum":
		return "(toreal(" + args[0] + ")+toreal(" + args[1] + "))"
	case "difference":
		return "(toreal(" + args[0] + ")-toreal(" + args[1] + "))"
	case "product":
		return "(toreal(" + args[0] + ")*toreal(" + args[1] + "))"
	case "quotient":
		return "iff(toreal(" + args[1] + ")==0.0,real(null),toreal(" + args[0] + ")/toreal(" + args[1] + "))"
	}
	panic("unvalidated analytical expression")
}

const traceKeyKQL = "Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId"

// itemSourceKQL flattens one approved collection of each trace's last
// agent-phase trajectory.ir event, mirroring the DuckDB item source. Missing
// keys, JSON null and invalid types stay distinct field states; anchor rows keep
// traces whose collection is absent, empty or invalid. Payload JSON is read only
// inside these let statements and never reaches the projected source.
func itemSourceKQL(path, key string, element sessionarchive.Column, events string) (string, string) {
	walk := "| extend __archive_has=true,__archive_value=parse_json(PayloadJSON)\n"
	for _, segment := range strings.Split(path, ".") {
		walk += "| extend __archive_has=__archive_has and gettype(__archive_value)==\"dictionary\" and bag_has_key(__archive_value," + literal(segment) + ")\n" +
			"| extend __archive_value=iff(__archive_has,__archive_value[" + literal(segment) + "],dynamic(null))\n"
	}
	lets := "let __archive_events=materialize(" + events + "| project " + traceKeyKQL +
		",Sequence,PolicyDigest,Gaps,OmissionReasons,__archive_ir=(Type==\"trajectory.ir\" and Phase==\"agent\"),PayloadJSON\n" +
		"| extend PayloadJSON=iff(__archive_ir,PayloadJSON,\"\"));\n" +
		"let __archive_traces=materialize(__archive_events\n" +
		// make_set flattens per-event Gaps into their distinct union.
		"| summarize PolicyDigest=tostring(array_sort_desc(make_set(PolicyDigest))[0]),Gaps=make_set(Gaps) by " + traceKeyKQL + "\n" +
		"| join kind=leftouter (__archive_events | where __archive_ir | summarize arg_max(Sequence,PayloadJSON,OmissionReasons) by " + traceKeyKQL + ") on " + traceKeyKQL + "\n" +
		"| project-away Namespace1,Repository1,RunId1,RunAttempt1,TraceId1,ProjectionId1\n" + walk +
		"| extend __archive_collection=iff(__archive_has,gettype(__archive_value),\"\")\n" +
		"| extend Items=iff(__archive_collection==\"array\",array_length(__archive_value),long(null)));\n"
	name := literal(element.Name)
	numeric := "(\"int\",\"long\",\"real\",\"decimal\")"
	var value, valid string
	present := "__archive_has_key"
	switch element.Type {
	case "integer":
		number := "iff(__archive_raw_kind in " + numeric + ",toreal(__archive_raw),real(null))"
		valid = "(isnotnull(" + number + ") and " + number + "==floor(" + number + ",1.0) and " + number + ">=0.0 and " + number + "<=9007199254740991.0)"
		value = "iff(" + valid + ",tolong(" + number + "),long(null))"
	case "boolean":
		valid = "(__archive_raw_kind==\"bool\")"
		value = "iff(" + valid + ",tobool(__archive_raw),bool(null))"
	default:
		// States are visits: a string element is its own id; a record contributes its string id.
		valid = "(__archive_raw_kind==\"string\")"
		value = "case(__archive_kind==\"string\",tostring(Element),__archive_kind==\"dictionary\" and " + valid + ",tostring(__archive_raw),\"\")"
		present = "(__archive_kind==\"string\" or __archive_has_key)"
	}
	fields := func(collection, item string) string {
		return "array_concat(iff(" + collection + ",pack_array(\"Items\"),dynamic([])),iff(" + item + ",pack_array(" + name + "),dynamic([])))"
	}
	source := "(union (__archive_traces | where coalesce(Items,0)==0 | extend Ordinal=long(null),Element=dynamic(null)),\n" +
		"(__archive_traces | where Items>0 | mv-expand with_itemindex=Ordinal Element=__archive_value to typeof(dynamic)))\n" +
		"| extend __archive_kind=iff(isnull(Ordinal),\"\",gettype(Element))\n" +
		"| extend __archive_has_key=__archive_kind==\"dictionary\" and bag_has_key(Element," + literal(key) + ")\n" +
		"| extend __archive_raw=iff(__archive_has_key,Element[" + literal(key) + "],dynamic(null))\n" +
		"| extend __archive_raw_kind=iff(__archive_has_key,gettype(__archive_raw),\"\")\n" +
		"| extend Kind=case(__archive_kind==\"dictionary\",\"object\",__archive_kind==\"array\",\"array\",__archive_kind==\"string\",\"string\"," +
		"__archive_kind==\"bool\",\"boolean\",__archive_kind==\"null\",\"null\",__archive_kind in " + numeric + ",\"number\",\"\")\n" +
		"| extend " + kqlIdentifier(element.Name) + "=" + value + ",\n" +
		"OmissionReasons=iff(isnull(OmissionReasons),dynamic([]),OmissionReasons),\n" +
		"PresentFields=" + fields("__archive_collection!=\"\"", present) + ",\n" +
		"NullFields=" + fields("__archive_collection==\"null\"", "__archive_raw_kind==\"null\"") + ",\n" +
		"InvalidFields=" + fields("__archive_collection!=\"\" and __archive_collection!=\"null\" and __archive_collection!=\"array\"",
		"(__archive_has_key and __archive_raw_kind!=\"null\" and not"+valid+")") + "\n" +
		"| project " + traceKeyKQL + ",PolicyDigest,Sequence,Ordinal,Items,Kind," + kqlIdentifier(element.Name) +
		",Gaps,OmissionReasons,PresentFields,NullFields,InvalidFields\n"
	return lets, source
}

// stageKQL lowers one plan's filter, reduce and post-aggregate phases. A
// chained stage reads the rows its predecessor produced in the same pipeline.
func stageKQL(plan sessionarchive.Plan) (string, error) {
	predicates := make([]string, 0, len(plan.Predicates))
	groups := make([]string, 0, len(plan.By))
	reductions := make([]string, 0, len(plan.Values))
	postReductions := []string{}
	ordered := false
	var order []sessionarchive.Column
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
		var filters []string
		for _, predicate := range value.Predicates {
			filters = append(filters, kqlPredicate(predicate))
		}
		filter := "true"
		if len(filters) > 0 {
			filter = strings.Join(filters, " and ")
		}
		valid := "(" + filter + " and " + present + ")"
		expression := "countif(" + valid + ")"
		isOrdered := value.Reducer == "first" || value.Reducer == "sum" && value.Field.Type != "integer"
		if isOrdered && plan.Input != nil {
			// Stage rows are unique by input key; every ordered value of one stage
			// shares the same remaining-key order, or the plan is not lowered.
			if value.Reducer == "first" || ordered && !slices.Equal(order, value.Order) {
				return "", sessionarchive.ErrQuery
			}
			order = value.Order
		}
		switch value.Reducer {
		case "distinct-count":
			// Exact and bounded by the pinned scan-row budget; never dcount().
			expression = "count_distinctif(" + field + "," + valid + ")"
		case "max":
			expression = "maxif(" + field + "," + valid + ")"
		case "sum":
			if value.Field.Type == "integer" {
				// Decimal accumulation covers the bounded million-row safe-count input;
				// one real conversion matches upstream BigInt -> Number, not long overflow.
				expression = "iff(countif(" + valid + ")==0,real(null),toreal(sumif(todecimal(" + field + ")," + valid + ")))"
			} else {
				expression = "make_list_if(" + field + "," + valid + ",1048576)"
				postReductions = append(postReductions, kqlIdentifier(value.As)+"=iff(array_length("+kqlIdentifier(value.As)+")==0,real(null),toreal(array_sum("+kqlIdentifier(value.As)+")))")
			}
		case "first":
			expression = "make_list_if(bag_pack('value'," + field + ")," + filter + ",1048576)"
			conversion := map[string]string{"integer": "tolong", "number": "toreal", "boolean": "tobool", "string": "tostring"}[value.Field.Type]
			postReductions = append(postReductions, kqlIdentifier(value.As)+"="+conversion+"("+kqlIdentifier(value.As)+"[0].value)")
		}
		ordered = ordered || isOrdered
		reductions = append(reductions, kqlIdentifier(value.As)+"="+expression)
	}
	kql := ""
	if len(predicates) > 0 {
		kql += "| where " + strings.Join(predicates, " and ") + "\n"
	}
	if len(reductions) > 0 {
		if ordered {
			sourceOrder := make([]string, 0, len(plan.By)+len(order)+1)
			for _, binding := range plan.By {
				sourceOrder = append(sourceOrder, kqlIdentifier(binding.Name)+" asc")
			}
			if plan.Input == nil {
				sourceOrder = append(sourceOrder, "Sequence asc")
			}
			var emptyKeys []string
			for _, key := range order {
				name := kqlIdentifier(key.Name)
				if key.Type == "string" {
					// Matches DuckDB nullif(key,'') ASC NULLS LAST.
					empty := kqlIdentifier("__archive_order_" + strconv.Itoa(len(emptyKeys)))
					emptyKeys = append(emptyKeys, empty+"=iff(isempty("+name+"),1,0)")
					sourceOrder = append(sourceOrder, empty+" asc")
				}
				sourceOrder = append(sourceOrder, name+" asc nulls last")
			}
			if len(emptyKeys) > 0 {
				kql += "| extend " + strings.Join(emptyKeys, ",") + "\n"
			}
			kql += "| order by " + strings.Join(sourceOrder, ",") + "\n| serialize\n"
		}
		kql += "| summarize " + strings.Join(reductions, ",")
		if len(groups) > 0 {
			kql += " by " + strings.Join(groups, ",")
		}
		kql += "\n"
	}
	for _, reduction := range postReductions {
		kql += "| extend " + reduction + "\n"
	}
	for _, computation := range plan.Computations {
		kql += "| extend " + kqlIdentifier(computation.As) + "=" + kqlExpression(computation.Expression) + "\n"
	}
	return kql, nil
}

// analyticalKQL lowers a reviewed plan over exact pinned events. Item sources
// and earlier chained stages lower into the same single query; omission
// coverage always counts the unfiltered base source.
func analyticalKQL(plan sessionarchive.Plan, events string) (string, error) {
	stages := []sessionarchive.Plan{}
	for stage := &plan; stage != nil; stage = stage.Input {
		stages = append([]sessionarchive.Plan{*stage}, stages...)
	}
	base := stages[0]
	lets, source := "", events
	if path, key, element, ok := sessionarchive.ItemSourceSpec(base.Source); ok {
		lets, source = itemSourceKQL(path, key, element, events)
	} else if base.Source != "archive-events" {
		return "", sessionarchive.ErrQuery
	}
	inputs := slices.Clone(base.Inputs)
	for _, name := range []string{"TraceId", "Gaps"} {
		if !slices.Contains(inputs, name) {
			inputs = append(inputs, name)
		}
	}
	projected := make([]string, 0, len(inputs))
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
	kql := lets + "let source=materialize(" + source + "| project " + strings.Join(projected, ",") + ");\nsource\n"
	for _, stage := range stages {
		text, err := stageKQL(stage)
		if err != nil {
			return "", err
		}
		kql += text
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
		"| project rows,tracesWithGaps", nil
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
	kql, err := analyticalKQL(plan, eventSource)
	if err != nil {
		return generation, sessionarchive.AnalyticalResult{}, nil, err
	}
	table, err := a.query(queryCtx, "/v1/rest/query", prefix+kql)
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
			switch {
			case column.Type == "string":
				if _, ok := value.(string); !ok {
					return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
				}
			case value == nil:
			case column.Type == "boolean":
				if _, ok := value.(bool); !ok {
					return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
				}
			default:
				n, ok := value.(json.Number)
				if !ok {
					return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
				}
				if column.Type == "integer" {
					if _, err := n.Int64(); err != nil {
						return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
					}
				} else {
					number, err := n.Float64()
					if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
						return generation, sessionarchive.AnalyticalResult{}, nil, ErrIntegrity
					}
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
		Metadata: definition.Metadata(limited, reader.pinned.admissionGaps, gaps)}
	return generation, result, reader, nil
}
