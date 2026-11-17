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
	case "trim":
		return "replace_regex(" + args[0] + ",@'" + sessionarchive.TrimPattern() + "','')"
	case "strip-prefix":
		return "replace_regex(" + args[0] + ",@'" + sessionarchive.StripPrefixPattern(e.Args[1].Literal.String, e.Args[2].Literal.Boolean) + "','')"
	case "keyword-match":
		return "(" + args[0] + " matches regex @'" + sessionarchive.KeywordPattern(e.Args[1].Literal.String) + "')"
	}
	panic("unvalidated analytical expression")
}

const traceKeyKQL = "Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId"

// itemTracesKQL binds each trace to its last agent-phase trajectory.ir
// payload once for every item source of a query.
func itemTracesKQL(events string) string {
	tools := make([]string, 0, len(sessionarchive.ReconciledToolTypes))
	for _, kind := range sessionarchive.ReconciledToolTypes {
		tools = append(tools, literal(kind))
	}
	return "let archive_events=materialize(" + events + "| project " + traceKeyKQL +
		",Sequence,PolicyDigest,Gaps,OmissionReasons,archive_ir=(Type==\"trajectory.ir\" and Phase==\"agent\"),PayloadJSON,\n" +
		"archive_synthesized=(Phase==\"agent\" and Component==\"agent\"),archive_tool=(Phase==\"agent\" and Type in (" + strings.Join(tools, ",") + "))\n" +
		"| extend PayloadJSON=iff(archive_ir,PayloadJSON,\"\"));\n" +
		"let archive_traces=materialize(archive_events\n" +
		// make_set flattens per-event Gaps into their distinct union.
		"| summarize PolicyDigest=tostring(array_sort_desc(make_set(PolicyDigest))[0]),Gaps=make_set(Gaps)," +
		"archive_synthesized=countif(archive_synthesized),archive_tools=countif(archive_tool) by " + traceKeyKQL + "\n" +
		"| join kind=leftouter (archive_events | where archive_ir | summarize arg_max(Sequence,PayloadJSON,OmissionReasons) by " + traceKeyKQL + ") on " + traceKeyKQL + "\n" +
		"| project-away Namespace1,Repository1,RunId1,RunAttempt1,TraceId1,ProjectionId1\n" + largestCollectionKQL() +
		"| extend PayloadJSON=" + markedPayloadKQL + ");\n"
}

// markedPayloadKQL prefixes every JSON string value, but not member names,
// with textMarker. parse_json turns date-, GUID- and timespan-like strings into
// datetime, guid and timespan values; marked strings are never converted, so
// item sources recover the exact text by removing the marker. Unescaped quotes
// in valid JSON only delimit strings, so the left-to-right string-token match
// starts at each opening quote; the second pass unmarks tokens followed by ":".
const markedPayloadKQL = `replace_regex(replace_regex(PayloadJSON,@'"((?:[^"\\]|\\.)*)"',@'"` + textMarker + `\1"'),` +
	`@'"` + textMarker + `((?:[^"\\]|\\.)*)"(\s*):',@'"\1"\2:')`

const textMarker = "#"

// largestCollectionKQL mirrors the DuckDB LargestCollection guard input:
// the longest guarded IR or reference array, with upstream's synthesized
// events and reconciled toolCalls bounds, null when a guarded input is
// excluded or reference is redacted.
func largestCollectionKQL() string {
	length := func(value string) string {
		return "iff(gettype(" + value + ")==\"array\",array_length(" + value + "),0)"
	}
	terms := []string{
		"iff(gettype(archive_data)==\"dictionary\" and bag_has_key(archive_data,\"events\")," + length(`archive_data["events"]`) + ",archive_synthesized)",
		"iff(gettype(archive_data[\"toolCalls\"])==\"null\",archive_tools," + length(`archive_data["toolCalls"]`) + ")",
		"iff(gettype(archive_reference)==\"dictionary\" and gettype(archive_reference[\"patch\"])==\"dictionary\"," + length(`archive_reference["patch"]["files"]`) + ",0)",
		"coalesce(archive_reference_max,0)",
	}
	for _, key := range sessionarchive.GuardedCollections {
		if key != "events" && key != "toolCalls" && key != "reference" {
			terms = append(terms, length("archive_data["+literal(key)+"]"))
		}
	}
	omitted := make([]string, 0, len(sessionarchive.GuardedOmissions()))
	for _, reason := range sessionarchive.GuardedOmissions() {
		omitted = append(omitted, literal(reason))
	}
	// The "" sentinel key keeps traces whose reference has no keys; reading a
	// real "" key only repeats a value already considered.
	return "| extend archive_payload=parse_json(PayloadJSON)\n" +
		"| extend archive_data=iff(gettype(archive_payload)==\"dictionary\" and bag_has_key(archive_payload,\"data\"),archive_payload[\"data\"],dynamic(null))\n" +
		"| extend archive_reference=iff(gettype(archive_data)==\"dictionary\",archive_data[\"reference\"],dynamic(null))\n" +
		"| extend archive_keys=array_concat(pack_array(\"\"),iff(gettype(archive_reference)==\"dictionary\",bag_keys(archive_reference),dynamic([])))\n" +
		"| mv-apply archive_key=archive_keys to typeof(string) on (summarize archive_reference_max=max(" +
		"iff(gettype(archive_reference)==\"dictionary\" and gettype(archive_reference[archive_key])==\"array\",array_length(archive_reference[archive_key]),0)))\n" +
		"| extend LargestCollection=iff(array_length(set_intersect(iff(isnull(OmissionReasons),dynamic([]),OmissionReasons),dynamic([" +
		strings.Join(omitted, ",") + "])))>0,long(null),tolong(max_of(" + strings.Join(terms, ",") + ")))\n" +
		"| project-away archive_payload,archive_data,archive_reference,archive_keys,archive_reference_max,archive_synthesized,archive_tools\n"
}

// itemSourceKQL flattens one approved collection of each trace's last
// agent-phase trajectory.ir event, mirroring the DuckDB item source. Missing
// keys, JSON null and invalid types stay distinct field states; anchor rows keep
// traces whose collection is absent, empty or invalid, and a nested source
// keeps one anchor for a trace without nested elements. Payload JSON is read
// only inside these let statements and never reaches the projected source.
func itemSourceKQL(item sessionarchive.ItemSource, index int) (string, string) {
	suffix := strconv.Itoa(index)
	collection := "archive_collection_" + suffix
	walk := "| extend archive_has=true,archive_value=parse_json(PayloadJSON)\n"
	for _, segment := range strings.Split(item.Path, ".") {
		walk += "| extend archive_has=archive_has and gettype(archive_value)==\"dictionary\" and bag_has_key(archive_value," + literal(segment) + ")\n" +
			"| extend archive_value=iff(archive_has,archive_value[" + literal(segment) + "],dynamic(null))\n"
	}
	lets := "let " + collection + "=materialize(archive_traces\n" + walk +
		"| extend archive_collection=iff(archive_has,gettype(archive_value),\"\")\n" +
		"| extend Items=iff(archive_collection==\"array\",array_length(archive_value),long(null)));\n"
	numeric := "(\"int\",\"long\",\"real\",\"decimal\")"
	parents := "(" + collection + " | where Items>0 | mv-expand with_itemindex=Ordinal Element=archive_value to typeof(dynamic))"
	source := "(union (" + collection + " | where coalesce(Items,0)==0 | extend Ordinal=long(null),Element=dynamic(null)),\n" + parents + ")\n"
	position := ""
	if item.Nested != "" {
		nested := "archive_nested_" + suffix
		lets += "let " + nested + "=materialize(" + parents + "\n" +
			"| extend archive_children=iff(gettype(Element)==\"dictionary\" and bag_has_key(Element," + literal(item.Nested) + ")," +
			"Element[" + literal(item.Nested) + "],dynamic(null))\n" +
			"| where gettype(archive_children)==\"array\" and array_length(archive_children)>0\n" +
			"| project-away Element\n| mv-expand with_itemindex=Position Element=archive_children to typeof(dynamic));\n"
		source = "(union " + nested + ",\n(" + collection + " | join kind=leftanti (" + nested + " | distinct " + traceKeyKQL + ") on " +
			traceKeyKQL + "\n| extend Ordinal=long(null),Position=long(null),Element=dynamic(null)))\n"
		position = "Position,"
	}
	// Marked strings (markedPayloadKQL) drop their one-character marker.
	text := func(raw string) string {
		return "substring(tostring(" + raw + "),1)"
	}
	source += "| extend archive_kind=iff(isnull(Ordinal),\"\",gettype(Element))\n" +
		"| extend Kind=case(archive_kind==\"dictionary\",\"object\",archive_kind==\"array\",\"array\",archive_kind==\"string\",\"string\"," +
		"archive_kind==\"bool\",\"boolean\",archive_kind==\"null\",\"null\",archive_kind in " + numeric + ",\"number\",\"\")\n"
	presents := make([]string, 0, 1+len(item.Elements))
	presents = append(presents, `iff(archive_collection!="",pack_array("Items"),dynamic([]))`)
	nulls := make([]string, 0, 1+len(item.Elements))
	nulls = append(nulls, `iff(archive_collection=="null",pack_array("Items"),dynamic([]))`)
	invalids := make([]string, 0, 1+len(item.Elements))
	invalids = append(invalids, `iff(archive_collection!="" and archive_collection!="null" and archive_collection!="array",pack_array("Items"),dynamic([]))`)
	columns := make([]string, 0, len(item.Elements))
	for i, element := range item.Elements {
		n := strconv.Itoa(i)
		has, raw, kind := "archive_has_key_"+n, "archive_raw_"+n, "archive_raw_kind_"+n
		if element.Key == "" {
			source += "| extend " + has + "=archive_kind!=\"\"\n| extend " + raw + "=Element\n"
		} else {
			source += "| extend " + has + "=archive_kind==\"dictionary\" and bag_has_key(Element," + literal(element.Key) + ")\n" +
				"| extend " + raw + "=iff(" + has + ",Element[" + literal(element.Key) + "],dynamic(null))\n"
		}
		source += "| extend " + kind + "=iff(" + has + ",gettype(" + raw + "),\"\")\n"
		var value, valid string
		present := has
		switch element.Typing {
		case "unsigned", "signed":
			low := "0.0"
			if element.Typing == "signed" {
				low = "-9007199254740991.0"
			}
			number := "iff(" + kind + " in " + numeric + ",toreal(" + raw + "),real(null))"
			valid = "(isnotnull(" + number + ") and " + number + "==floor(" + number + ",1.0) and " + number + ">=" + low + " and " + number + "<=9007199254740991.0)"
			value = "iff(" + valid + ",tolong(" + number + "),long(null))"
		case "boolean":
			valid = "(" + kind + "==\"bool\")"
			value = "iff(" + valid + ",tobool(" + raw + "),bool(null))"
		case "length":
			valid = "(" + kind + "==\"array\")"
			value = "iff(" + valid + ",array_length(" + raw + "),long(null))"
		case "visit":
			// A string element is its own value; a record contributes its string Key.
			valid = "(" + kind + "==\"string\")"
			value = "case(archive_kind==\"string\"," + text("Element") + ",archive_kind==\"dictionary\" and " + valid + "," + text(raw) + ",\"\")"
			present = "(archive_kind==\"string\" or " + has + ")"
		default:
			valid = "(" + kind + "==\"string\")"
			value = "iff(" + valid + "," + text(raw) + ",\"\")"
		}
		name := literal(element.Column.Name)
		member := func(condition string) string {
			return "iff(" + condition + ",pack_array(" + name + "),dynamic([]))"
		}
		source += "| extend " + kqlIdentifier(element.Column.Name) + "=" + value + "\n"
		columns = append(columns, kqlIdentifier(element.Column.Name))
		presents = append(presents, member(present))
		nulls = append(nulls, member(kind+"==\"null\""))
		invalids = append(invalids, member("("+has+" and "+kind+"!=\"null\" and not"+valid+")"))
	}
	source += "| extend OmissionReasons=iff(isnull(OmissionReasons),dynamic([]),OmissionReasons),\n" +
		"PresentFields=array_concat(" + strings.Join(presents, ",") + "),\n" +
		"NullFields=array_concat(" + strings.Join(nulls, ",") + "),\n" +
		"InvalidFields=array_concat(" + strings.Join(invalids, ",") + ")\n" +
		"| project " + traceKeyKQL + ",PolicyDigest,Sequence,Ordinal,Items,Kind,LargestCollection," + position + strings.Join(columns, ",") +
		",Gaps,OmissionReasons,PresentFields,NullFields,InvalidFields\n"
	return lets, source
}

// joinKQL lowers one trace-scoped join with an explicit kind (Kusto's
// default innerunique would deduplicate left rows). Right rows with a
// missing explicit key are removed first because Kusto matches "" with "".
func joinKQL(join sessionarchive.PlannedJoin, right string) string {
	projected := make([]string, 0, len(join.On)+len(join.Fields))
	conditions := make([]string, 0, len(join.On))
	var present []string
	for i, key := range join.On {
		alias := "archive_key_" + strconv.Itoa(i)
		projected = append(projected, alias+"="+kqlIdentifier(key.Right.Name))
		conditions = append(conditions, "$left."+kqlIdentifier(key.Left.Name)+"==$right."+alias)
		if !key.Binding {
			check := "isnotnull("
			if key.Right.Type == "string" {
				check = "isnotempty("
			}
			present = append(present, check+kqlIdentifier(key.Right.Name)+")")
		}
	}
	for _, field := range join.Fields {
		projected = append(projected, kqlIdentifier(field.As)+"="+kqlIdentifier(field.Field.Name))
	}
	filter := ""
	if len(present) > 0 {
		filter = " | where " + strings.Join(present, " and ")
	}
	kind := "inner"
	if join.Type == "left" {
		kind = "leftouter"
	}
	return "| join kind=" + kind + " (" + right + filter + " | project " + strings.Join(projected, ",") + ") on " +
		strings.Join(conditions, ",") + "\n| project-away archive_key_*\n"
}

// stageKQL lowers one plan's join, filter, reduce and post-aggregate phases
// over its input, joining rights in join order.
func stageKQL(plan sessionarchive.Plan, rights []string) (string, error) {
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
	for i, join := range plan.Joins {
		kql += joinKQL(join, rights[i])
	}
	if len(predicates) > 0 {
		kql += "| where " + strings.Join(predicates, " and ") + "\n"
	}
	kql += rowKQL(plan)
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
					empty := kqlIdentifier("archive_order_" + strconv.Itoa(len(emptyKeys)))
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

// rowKQL lowers row-level compute and previous/row-number windows. Each
// window sorts globally by partition then order keys, restarting its row
// number where any partition value changes; the planner guarantees groupby
// plus order-by covers the row key, so the order is total.
func rowKQL(plan sessionarchive.Plan) string {
	kql := ""
	for _, computation := range plan.RowComputations {
		kql += "| extend " + kqlIdentifier(computation.As) + "=" + kqlExpression(computation.Expression) + "\n"
	}
	for _, window := range plan.Windows {
		var order, empties []string
		same := make([]string, 0, len(window.Partition))
		for _, column := range window.Partition {
			name := kqlIdentifier(column.Name)
			order = append(order, name+" asc nulls last")
			equal := "coalesce(" + name + "==prev(" + name + "),false)"
			if column.Type != "string" {
				equal = "(" + equal + " or (isnull(" + name + ") and isnull(prev(" + name + "))))"
			}
			same = append(same, equal)
		}
		for _, key := range window.Order {
			name := kqlIdentifier(key.Field.Name)
			if key.Field.Type == "string" {
				// Matches DuckDB nullif(key,'') NULLS LAST in either direction.
				empty := kqlIdentifier("archive_window_" + strconv.Itoa(len(empties)))
				empties = append(empties, empty+"=iff(isempty("+name+"),1,0)")
				order = append(order, empty+" asc")
			}
			order = append(order, name+" "+key.Direction+" nulls last")
		}
		if len(empties) > 0 {
			kql += "| extend " + strings.Join(empties, ",") + "\n"
		}
		kql += "| order by " + strings.Join(order, ",") + "\n" +
			"| extend archive_restart=row_number()==1 or not(" + strings.Join(same, " and ") + ")\n"
		as := kqlIdentifier(window.As)
		if window.Operation == "row-number" {
			kql += "| extend " + as + "=row_number(1,archive_restart)\n"
		} else {
			first := map[string]string{"number": "real(null)", "integer": "long(null)", "boolean": "bool(null)", "string": `""`}[window.Type]
			kql += "| extend " + as + "=iff(archive_restart," + first + ",prev(" + kqlIdentifier(window.Field.Name) + "))\n"
		}
	}
	return kql
}

// analyticalKQL lowers a reviewed plan over exact pinned events. Item
// sources and earlier stages lower into materialized lets of one query;
// omission coverage always counts the unfiltered base source.
func analyticalKQL(plan sessionarchive.Plan, events string) (string, error) {
	names, sourceInputs := plan.SourceInputs()
	kql := ""
	read := map[string]string{}
	for i, name := range names {
		source := events
		if item, ok := sessionarchive.ItemSourceSpec(name); ok {
			if !strings.Contains(kql, "let archive_traces=") {
				kql += itemTracesKQL(events)
			}
			var lets string
			lets, source = itemSourceKQL(item, i)
			kql += lets
		} else if name != "archive-events" {
			return "", sessionarchive.ErrQuery
		}
		read[name] = "source_" + strconv.Itoa(i)
		projected := make([]string, 0, len(sourceInputs[name]))
		for _, input := range sourceInputs[name] {
			projected = append(projected, kqlIdentifier(input))
		}
		kql += "let " + read[name] + "=materialize(" + source + "| project " + strings.Join(projected, ",") + ");\n"
	}
	outputs := map[*sessionarchive.Plan]string{}
	lower := func(stage *sessionarchive.Plan) (string, error) {
		from, rights := read[stage.Source], make([]string, 0, len(stage.Joins))
		if stage.Input != nil {
			from = outputs[stage.Input]
		}
		for _, join := range stage.Joins {
			right := read[join.Source]
			if join.Input != nil {
				right = outputs[join.Input]
			}
			rights = append(rights, right)
		}
		text, err := stageKQL(*stage, rights)
		return from + "\n" + text, err
	}
	dependencies := plan.Dependencies()
	for i, stage := range dependencies[:len(dependencies)-1] {
		text, err := lower(stage)
		if err != nil {
			return "", err
		}
		if !stage.Aggregate() {
			// A row-level stage exposes only its selected row, including its key.
			selected := make([]string, 0, len(stage.Selections))
			for _, selection := range stage.Selections {
				selected = append(selected, kqlIdentifier(selection.As)+"="+kqlIdentifier(selection.Field.Name))
			}
			text += "| project " + strings.Join(selected, ",") + "\n"
		}
		outputs[stage] = "stage_" + strconv.Itoa(i)
		kql += "let " + outputs[stage] + "=materialize(" + text + ");\n"
	}
	outer, err := lower(&plan)
	if err != nil {
		return "", err
	}
	kql += outer
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
		"| extend tracesWithGaps=toscalar(source_0 | where array_length(Gaps)>0 | summarize by TraceId | count)\n" +
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
