package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

type SQLKind string

const (
	SQLText       SQLKind = "text"
	SQLNumber     SQLKind = "number"
	SQLBoolean    SQLKind = "boolean"
	SQLTimestamp  SQLKind = "timestamp"
	SQLStructured SQLKind = "structured"
	sqlNull       SQLKind = "null"
)

// SQLColumn describes a typed relation column, not a persisted row document.
type SQLColumn struct {
	Expression string
	Presence   string
	Kind       SQLKind
	Point      string
}

type SQLRelation struct {
	SQL     string
	Columns map[string]SQLColumn
	Order   string
	Params  []any
}

type SQLSourceResolver func(string) (SQLRelation, error)

type SQLStep struct {
	Query     string
	Relation  string
	Operation string
	Weight    int
}

type SQLPlan struct {
	CTEs     string
	CTEList  []string
	Outputs  map[string]SQLRelation
	Args     []any
	Steps    []SQLStep
	JoinKeys []string
}

type sqlCompiler struct {
	args  []any
	ctes  []string
	steps []SQLStep
	joins []string
	next  int
	err   error
}

// CompileSQL lowers a validated declarative DAG to typed relational SQL.
// Structured SQL values are wire projections only; this compiler creates no
// stored JSON documents and has no row-evaluator fallback.
func CompileSQL(definitions []Definition, requested []string, resolve SQLSourceResolver) (SQLPlan, error) {
	return compileSQL(definitions, requested, resolve, "", 0)
}

// CompileSQLPage keeps boundary lookahead in the boundary output, but prevents
// downstream consumers from deriving rows from outside the requested page.
func CompileSQLPage(definitions []Definition, requested []string, resolve SQLSourceResolver, boundary string, limit int) (SQLPlan, error) {
	if boundary == "" || limit <= 0 || limit > 32 {
		return SQLPlan{}, errors.New("invalid SQL page boundary")
	}
	return compileSQL(definitions, requested, resolve, boundary, limit)
}

func compileSQL(definitions []Definition, requested []string, resolve SQLSourceResolver, boundary string, limit int) (SQLPlan, error) {
	compiler := sqlCompiler{}
	relations := map[string]SQLRelation{}
	var boundaryOutput SQLRelation
	load := func(name string) error {
		if relations[name].SQL != "" {
			return nil
		}
		relation, err := resolve(name)
		if err != nil {
			return err
		}
		builder := sqlbuilder.New(compiler.args...)
		builder.Write(relation.SQL, relation.Params...)
		sourceSQL, arguments, err := builder.Statement()
		if err != nil {
			return err
		}
		compiler.args = arguments
		relation.SQL = sourceSQL
		relations[name] = compiler.materialize(relation, name, "source", 0)
		return nil
	}
	err := WalkPlan(definitions, requested, load, func(definition Definition) error {
		relation, err := compiler.definition(definition, relations)
		if err != nil {
			return fmt.Errorf("query %q: %w", definition.Name, err)
		}
		relations[definition.Name] = relation
		if definition.Name == boundary {
			boundaryOutput = relation
			relation.SQL += " ORDER BY " + relation.Order + " LIMIT " + compiler.bind(limit)
			relations[definition.Name] = compiler.materialize(relation, definition.Name, "page", 0)
		}
		return nil
	})
	if err != nil {
		return SQLPlan{}, err
	}
	if boundary != "" && boundaryOutput.SQL == "" {
		return SQLPlan{}, errors.New("SQL page boundary is unavailable")
	}
	outputs := make(map[string]SQLRelation, len(requested))
	for _, name := range requested {
		outputs[name] = relations[name]
		if name == boundary {
			outputs[name] = boundaryOutput
		}
	}
	if compiler.err != nil {
		return SQLPlan{}, compiler.err
	}
	return SQLPlan{CTEs: "WITH " + strings.Join(compiler.ctes, ",\n"), CTEList: compiler.ctes, Outputs: outputs,
		Args: compiler.args, Steps: compiler.steps, JoinKeys: compiler.joins}, nil
}

func SQLIdentifier(name string) string {
	return sqlbuilder.QuoteIdentifier(name)
}

func sortedSQLFields(columns map[string]SQLColumn) []string {
	fields := make([]string, 0, len(columns))
	for field := range columns {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func (c *sqlCompiler) bind(value any) string {
	if text, ok := value.(string); ok && strings.ContainsRune(text, 0) {
		// Postgres text cannot hold NUL, so such a value can never equal stored
		// data; the replacement keeps comparisons well-formed and non-matching.
		value = strings.ReplaceAll(text, "\x00", "\uFFFD")
	}
	c.args = append(c.args, value)
	return "$" + strconv.Itoa(len(c.args))
}

func (c *sqlCompiler) materialize(input SQLRelation, query, operation string, weight int) SQLRelation {
	name := "q" + strconv.Itoa(c.next)
	c.next++
	columns := make(map[string]SQLColumn, len(input.Columns))
	projection := make([]string, 0, 1+len(input.Columns)*2)
	projection = append(projection, input.Order+` AS "__order"`)
	for index, field := range sortedSQLFields(input.Columns) {
		column := input.Columns[field]
		valueName, presentName := fmt.Sprintf("v%d", index), fmt.Sprintf("p%d", index)
		projection = append(projection, column.Expression+" AS "+SQLIdentifier(valueName),
			column.Presence+" AS "+SQLIdentifier(presentName))
		columns[field] = SQLColumn{Expression: SQLIdentifier(valueName), Presence: SQLIdentifier(presentName), Kind: column.Kind, Point: column.Point}
	}
	statement, _, err := sqlbuilder.Build("{} AS NOT MATERIALIZED (SELECT {} FROM {})",
		sqlbuilder.Identifier(name), sqlbuilder.Fragment(strings.Join(projection, ",")), sqlbuilder.Fragment(input.SQL))
	if err != nil {
		c.err = err
		return SQLRelation{}
	}
	c.ctes = append(c.ctes, statement)
	c.steps = append(c.steps, SQLStep{Query: query, Relation: name, Operation: operation, Weight: weight})
	return SQLRelation{SQL: SQLIdentifier(name), Columns: columns, Order: `"__order"`}
}

func qualifyRelation(relation SQLRelation, alias string) SQLRelation {
	columns := make(map[string]SQLColumn, len(relation.Columns))
	for field, column := range relation.Columns {
		columns[field] = SQLColumn{Expression: alias + "." + column.Expression,
			Presence: alias + "." + column.Presence, Kind: column.Kind, Point: column.Point}
	}
	return SQLRelation{SQL: relation.SQL + " AS " + alias, Columns: columns, Order: alias + "." + relation.Order}
}

// computeReferencesBatchField reports whether computed reads a field
// produced earlier in the same uncommitted compute batch. It is extracted
// from the inline loop in definition so the batch-split boundary is
// independently testable against plain field names, without constructing a
// SQLRelation or running the compiler.
func computeReferencesBatchField(computed ComputedField, batch map[string]bool) bool {
	for _, argument := range computed.Args {
		if argument.Field != nil && batch[*argument.Field] {
			return true
		}
	}
	return false
}

func (c *sqlCompiler) definition(definition Definition, relations map[string]SQLRelation) (SQLRelation, error) {
	relation := relations[definition.From]
	if relation.SQL == "" {
		return SQLRelation{}, errors.New("input relation is unavailable")
	}
	columns := make(map[string]SQLColumn, len(relation.Columns))
	for field, column := range relation.Columns {
		columns[field] = column
	}
	relation.Columns = columns
	if len(definition.Union) != 0 {
		inputs := []SQLRelation{relation}
		for _, source := range definition.Union {
			if relations[source].SQL == "" {
				return SQLRelation{}, errors.New("union relation is unavailable")
			}
			inputs = append(inputs, relations[source])
		}
		var err error
		relation, err = c.union(inputs, definition.Name)
		if err != nil {
			return SQLRelation{}, err
		}
	}
	c.steps = append(c.steps, SQLStep{Query: definition.Name, Relation: strings.Trim(relation.SQL, `"`), Operation: "from", Weight: 1})
	for _, join := range definition.Joins {
		var err error
		relation, err = c.join(relation, relations[join.Source], join, definition.Name)
		if err != nil {
			return SQLRelation{}, err
		}
	}
	if definition.Filter != nil {
		c.steps = append(c.steps, SQLStep{Query: definition.Name, Relation: strings.Trim(relation.SQL, `"`), Operation: "filter", Weight: 1})
		predicate, err := c.filter(relation, definition.Filter)
		if err != nil {
			return SQLRelation{}, err
		}
		relation.SQL += " WHERE " + predicate
		relation = c.materialize(relation, definition.Name, "filter-output", 0)
	}
	// Computed fields share one materialization until one reads a field
	// computed in the same batch, which keeps wide relations from being
	// re-projected once per field.
	batch := map[string]bool{}
	for _, computed := range definition.Compute {
		if computeReferencesBatchField(computed, batch) {
			queryLog.Printf("compute batch materialized query=%s batch_size=%d", definition.Name, len(batch))
			relation = c.materialize(relation, definition.Name, "compute-output", 0)
			batch = map[string]bool{}
		}
		c.steps = append(c.steps, SQLStep{Query: definition.Name, Relation: strings.Trim(relation.SQL, `"`), Operation: "compute", Weight: 1})
		column, err := c.compute(relation, computed)
		if err != nil {
			return SQLRelation{}, err
		}
		relation.Columns[computed.As] = column
		batch[computed.As] = true
	}
	if len(batch) != 0 {
		relation = c.materialize(relation, definition.Name, "compute-output", 0)
	}
	if definition.TemporalSeries != nil {
		var err error
		relation, err = c.temporal(relation, *definition.TemporalSeries, definition.Name)
		if err != nil {
			return SQLRelation{}, err
		}
	}
	if definition.Aggregate != nil {
		var err error
		relation, err = c.aggregate(relation, definition.Aggregate, definition.Name)
		if err != nil {
			return SQLRelation{}, err
		}
	}
	for _, entry := range definition.Window {
		var err error
		relation, err = c.window(relation, entry, definition.Name)
		if err != nil {
			return SQLRelation{}, err
		}
	}
	if len(definition.Select) != 0 {
		c.steps = append(c.steps, SQLStep{Query: definition.Name, Relation: strings.Trim(relation.SQL, `"`), Operation: "select", Weight: 1})
		columns := make(map[string]SQLColumn, len(definition.Select))
		for _, selected := range definition.Select {
			column := sqlField(relation, selected.Field)
			name := selected.Field
			if selected.As != "" {
				name = selected.As
			}
			if _, duplicate := columns[name]; duplicate {
				return SQLRelation{}, errors.New("duplicate projection alias")
			}
			columns[name] = column
		}
		relation.Columns = columns
		relation = c.materialize(relation, definition.Name, "select-output", 0)
	}
	if len(definition.OrderBy) != 0 {
		order := make([]string, 0, len(definition.OrderBy)+1)
		for _, field := range definition.OrderBy {
			column := sqlField(relation, field.Field)
			direction := "ASC"
			if field.Direction == "desc" {
				direction = "DESC"
			}
			order = append(order, column.Expression+" "+direction+" NULLS FIRST")
		}
		order = append(order, relation.Order)
		relation.Order = "row_number() OVER (ORDER BY " + strings.Join(order, ",") + ")"
		relation = c.materialize(relation, definition.Name, "order-by", 1)
	}
	if definition.Limit != nil {
		relation.SQL += " ORDER BY " + relation.Order + " LIMIT " + c.bind(*definition.Limit)
		relation = c.materialize(relation, definition.Name, "limit", 0)
	}
	if definition.Facet != nil {
		var err error
		relation, err = c.facet(relation, *definition.Facet, definition.Name)
		if err != nil {
			return SQLRelation{}, err
		}
	}
	return relation, nil
}

func sqlField(relation SQLRelation, field string) SQLColumn {
	column, exists := relation.Columns[field]
	if !exists {
		// Fields absent from the relation are missing values, matching the
		// dashboard engine's treatment of fields a source does not provide.
		return SQLColumn{Expression: "NULL::text", Kind: sqlNull, Presence: "FALSE"}
	}
	return column
}

func sqlText(column SQLColumn) string {
	if column.Kind == SQLStructured || column.Kind == sqlNull {
		return "''::text"
	}
	if column.Kind == SQLTimestamp {
		return "coalesce(to_char((" + column.Expression + ") AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"'), '')"
	}
	if column.Kind == SQLNumber {
		return "coalesce(trim_scale((" + column.Expression + ")::numeric)::text, '')"
	}
	return "coalesce((" + column.Expression + ")::text, '')"
}

func sqlNumeric(column SQLColumn) string {
	if column.Kind == SQLNumber {
		return "(" + column.Expression + ")::numeric"
	}
	if column.Kind != SQLText {
		return "NULL::numeric"
	}
	text := "trim(" + sqlText(column) + ")"
	return "CASE WHEN length(" + text + ") < 400 AND " + text +
		` ~ '^[+-]?([0-9]+([.][0-9]*)?|[.][0-9]+)([eE][+-]?[0-9]{1,3})?$' THEN (` + text + ")::numeric ELSE NULL::numeric END"
}

func sqlFinite(expression string) string {
	return "CASE WHEN abs(" + expression + ") <= 1.7976931348623157e308::numeric THEN (" + expression + ") ELSE NULL::numeric END"
}

func (c *sqlCompiler) literal(value any) (SQLColumn, error) {
	column := SQLColumn{Presence: "TRUE"}
	switch v := value.(type) {
	case nil:
		column.Expression, column.Kind = "NULL::text", sqlNull
	case string:
		column.Expression, column.Kind = c.bind(v)+"::text", SQLText
	case bool:
		column.Expression, column.Kind = c.bind(v)+"::boolean", SQLBoolean
	case float64:
		column.Expression, column.Kind = c.bind(v)+"::numeric", SQLNumber
	case int:
		column.Expression, column.Kind = c.bind(v)+"::numeric", SQLNumber
	case json.Number:
		column.Expression, column.Kind = c.bind(string(v))+"::numeric", SQLNumber
	default:
		return SQLColumn{}, errors.New("query literal must be scalar")
	}
	return column, nil
}

func sqlEquals(left, right SQLColumn, unknown bool) string {
	equal := "((" + left.Expression + ") IS NOT NULL AND (" + right.Expression + ") IS NOT NULL AND " + sqlText(left) + " = " + sqlText(right) + ")"
	if unknown {
		equal = "((" + left.Expression + ") IS NULL OR " + equal + ")"
	}
	if right.Kind == sqlNull {
		equal = "((" + left.Expression + ") IS NULL)"
	}
	return equal
}

func (c *sqlCompiler) filter(relation SQLRelation, filter *Filter) (string, error) {
	if filter == nil {
		return "TRUE", nil
	}
	predicates := make([]string, 0, len(filter.Predicates)+1)
	for _, predicate := range filter.Predicates {
		column := sqlField(relation, predicate.Field)
		if column.Kind == SQLStructured {
			return "", errors.New("structured fields cannot be filtered")
		}
		var terms []string
		for _, candidate := range predicate.In {
			right, err := c.literal(candidate)
			if err != nil {
				return "", err
			}
			terms = append(terms, sqlEquals(column, right, candidate == "unknown"))
		}
		switch {
		case len(terms) != 0:
			terms = []string{"(" + strings.Join(terms, " OR ") + ")"}
		case predicate.Includes != "":
			terms = []string{"position(lower(" + c.bind(predicate.Includes) + "::text) IN lower(" + sqlText(column) + ")) > 0"}
		case predicate.GTE != nil || predicate.LT != nil:
			for _, boundary := range []struct {
				value    any
				operator string
			}{{predicate.GTE, ">="}, {predicate.LT, "<"}} {
				if boundary.value == nil {
					continue
				}
				right, err := c.literal(boundary.value)
				if err != nil {
					return "", err
				}
				leftExpression, rightExpression := sqlText(column), sqlText(right)
				if column.Kind == SQLNumber {
					leftExpression, rightExpression = sqlNumeric(column), sqlNumeric(right)
				}
				terms = append(terms, "("+column.Expression+") IS NOT NULL AND ("+leftExpression+" "+boundary.operator+" "+rightExpression+")")
			}
		default:
			right, err := c.literal(predicate.Equals)
			if err != nil {
				return "", err
			}
			terms = []string{sqlEquals(column, right, predicate.Equals == "unknown")}
		}
		condition := "(" + strings.Join(terms, " AND ") + ")"
		if predicate.Optional {
			condition = "((" + column.Expression + ") IS NULL OR " + sqlText(column) + " = '' OR " + condition + ")"
		}
		predicates = append(predicates, condition)
	}
	if filter.Search != nil && strings.TrimSpace(filter.Search.Query) != "" {
		terms := make([]string, 0, len(filter.Search.Fields))
		search := c.bind(strings.ToLower(strings.TrimSpace(filter.Search.Query))) + "::text"
		for _, field := range filter.Search.Fields {
			column := sqlField(relation, field)
			if column.Kind == SQLStructured {
				return "", errors.New("structured fields cannot be searched")
			}
			terms = append(terms, "position("+search+" IN lower("+sqlText(column)+")) > 0")
		}
		if len(terms) == 0 {
			predicates = append(predicates, "FALSE")
		} else {
			predicates = append(predicates, "("+strings.Join(terms, " OR ")+")")
		}
	}
	if len(predicates) == 0 {
		return "TRUE", nil
	}
	return strings.Join(predicates, " AND "), nil
}

func compatibleSQLKind(left, right SQLKind) (SQLKind, error) {
	if left == sqlNull {
		return right, nil
	}
	if right == sqlNull || left == right {
		return left, nil
	}
	return "", errors.New("SQL expression branches have incompatible declared types")
}

func castSQLColumn(column SQLColumn, kind SQLKind) string {
	if column.Kind != sqlNull {
		return column.Expression
	}
	types := map[SQLKind]string{SQLText: "text", SQLNumber: "numeric", SQLBoolean: "boolean", SQLTimestamp: "timestamptz", SQLStructured: "jsonb", sqlNull: "text"}
	return "NULL::" + types[kind]
}

func (c *sqlCompiler) compute(relation SQLRelation, computed ComputedField) (SQLColumn, error) {
	args := make([]SQLColumn, len(computed.Args))
	for index, argument := range computed.Args {
		var err error
		switch {
		case argument.Field != nil:
			args[index] = sqlField(relation, *argument.Field)
		case argument.Context != "" || argument.Parameter != "":
			return SQLColumn{}, errors.New("query execution context and parameters must be resolved before compilation")
		default:
			args[index], err = c.literal(argument.Value)
		}
		if err != nil {
			return SQLColumn{}, err
		}
	}
	out := SQLColumn{Presence: "TRUE"}
	switch computed.Function {
	case "literal":
		return args[0], nil
	case "concat":
		parts := make([]string, len(args))
		for index, argument := range args {
			parts[index] = sqlText(argument)
		}
		out.Expression, out.Kind = "("+strings.Join(parts, " || ")+")", SQLText
	case "coalesce":
		out.Kind = sqlNull
		for _, argument := range args {
			if argument.Kind == SQLStructured {
				continue
			}
			var err error
			out.Kind, err = compatibleSQLKind(out.Kind, argument.Kind)
			if err != nil {
				return SQLColumn{}, err
			}
		}
		var parts []string
		for _, argument := range args {
			if argument.Kind == SQLStructured {
				continue
			}
			expression := castSQLColumn(argument, out.Kind)
			if out.Kind == SQLText {
				expression = "nullif(" + expression + ", '')"
			}
			parts = append(parts, expression)
		}
		if len(parts) == 0 {
			out.Expression = "NULL::text"
		} else {
			out.Expression = "coalesce(" + strings.Join(parts, ",") + ")"
		}
	case "lower", "upper":
		out.Expression, out.Kind = computed.Function+"("+sqlText(args[0])+")", SQLText
	case "trim":
		out.Expression, out.Kind = "trim("+sqlText(args[0])+")", SQLText
	case "title-case":
		out.Expression, out.Kind = "(SELECT coalesce(string_agg(upper(left(word,1)) || substr(word,2), ' ' ORDER BY position), '') "+
			"FROM regexp_split_to_table("+sqlText(args[0])+", '[-_]') WITH ORDINALITY AS words(word, position) WHERE word <> '')", SQLText
	case "url-encode":
		input := "convert_to(" + sqlText(args[0]) + ", 'UTF8')"
		out.Expression, out.Kind = "(SELECT coalesce(string_agg(CASE WHEN get_byte("+input+", position) IN "+
			"(33,39,40,41,42,45,46,95,126) OR get_byte("+input+", position) BETWEEN 48 AND 57 OR get_byte("+input+
			", position) BETWEEN 65 AND 90 OR get_byte("+input+", position) BETWEEN 97 AND 122 THEN chr(get_byte("+input+
			", position)) ELSE '%' || upper(lpad(to_hex(get_byte("+input+", position)),2,'0')) END, '' ORDER BY position), '') "+
			"FROM generate_series(0, octet_length("+input+")-1) AS bytes(position))", SQLText
	case "replace-suffix":
		input, suffix, replacement := sqlText(args[0]), sqlText(args[1]), sqlText(args[2])
		out.Expression, out.Kind = "CASE WHEN "+suffix+" <> '' AND right("+input+", length("+suffix+")) = "+suffix+
			" THEN left("+input+", length("+input+")-length("+suffix+")) || "+replacement+" ELSE "+input+" END", SQLText
	case "equals-any":
		var conditions []string
		for index, argument := range args[1:] {
			conditions = append(conditions, sqlEquals(args[0], argument, computed.Args[index+1].Value == "unknown"))
		}
		out.Expression, out.Kind = "("+strings.Join(conditions, " OR ")+")", SQLBoolean
	case "if":
		kind, err := compatibleSQLKind(args[1].Kind, args[2].Kind)
		if err != nil {
			return SQLColumn{}, err
		}
		out.Kind = kind
		condition := "FALSE"
		if args[0].Kind == SQLBoolean {
			condition = "(" + args[0].Expression + ") IS TRUE"
		}
		out.Expression = "CASE WHEN " + condition + " THEN " + castSQLColumn(args[1], kind) + " ELSE " + castSQLColumn(args[2], kind) + " END"
	case "number":
		out.Expression, out.Kind = sqlFinite(sqlNumeric(args[0])), SQLNumber
	case "greater-than":
		out.Expression, out.Kind = "("+sqlNumeric(args[0])+" > "+sqlNumeric(args[1])+")", SQLBoolean
	case "sum", "difference", "product", "quotient":
		operator := map[string]string{"sum": "+", "difference": "-", "product": "*", "quotient": "/"}[computed.Function]
		parts := make([]string, len(args))
		for index, argument := range args {
			parts[index] = "(" + sqlNumeric(argument) + ")"
		}
		if computed.Function == "quotient" {
			parts[1] = "nullif(" + parts[1] + ", 0)"
		}
		out.Expression, out.Kind = sqlFinite("("+strings.Join(parts, operator)+")"), SQLNumber
	case "positive-integer":
		value := sqlNumeric(args[0])
		out.Expression, out.Kind = "CASE WHEN "+value+" > 0 AND "+value+" = trunc("+value+") THEN "+value+" ELSE "+sqlNumeric(args[1])+" END", SQLNumber
	case "timestamp":
		at := args[0].Expression
		if args[0].Kind == SQLText {
			const pattern = `^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]+)?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`
			at = "CASE WHEN " + at + " ~ '" + pattern + "' AND left(" + at + ", 4) <> '0000' AND pg_input_is_valid(" + at + ", 'timestamp with time zone') THEN (" + at + ")::timestamptz ELSE NULL::timestamptz END"
		} else if args[0].Kind != SQLTimestamp {
			out.Expression, out.Kind = "NULL::numeric", SQLNumber
			break
		}
		out.Expression, out.Kind = "floor(extract(epoch FROM "+at+") * 1000)", SQLNumber
	case "iso-timestamp":
		value := sqlNumeric(args[0])
		bounded := "(CASE WHEN " + value + " BETWEEN -62135596800000 AND 253402300799999 THEN trunc(" + value + ") END)"
		out.Expression, out.Kind = "to_char((to_timestamp(trunc("+bounded+"/1000)) + mod("+bounded+",1000) * INTERVAL '1 millisecond') AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.MS\"Z\"')", SQLText
	case "sqrt":
		value := sqlNumeric(args[0])
		out.Expression, out.Kind = "sqrt(CASE WHEN "+value+" >= 0 THEN "+value+" END)", SQLNumber
	case "date-day":
		if args[0].Kind != SQLTimestamp {
			return SQLColumn{}, errors.New("date-day requires a typed timestamp")
		}
		out.Expression, out.Kind = "to_char("+args[0].Expression+" AT TIME ZONE 'UTC', 'YYYY-MM-DD')", SQLText
	case "activity-shard-day":
		const pattern = `^(?:[^?#]*/)?([0-9]{4}-[0-9]{2}-[0-9]{2})-[0-9]{4,}-[a-fA-F0-9]{16}\.jsonl(?:[?#].*)?$`
		day := "substring(" + sqlText(args[0]) + " from '" + pattern + "')"
		out.Expression, out.Kind = "CASE WHEN left("+day+", 4) <> '0000' AND pg_input_is_valid("+day+
			", 'date') THEN "+day+" ELSE NULL::text END", SQLText
	case "date-bucket":
		unit, ok := computed.Args[1].Value.(string)
		if !ok || (unit != "hour" && unit != "day" && unit != "week" && unit != "month") {
			return SQLColumn{}, errors.New("date-bucket requires a supported literal UTC unit")
		}
		at := args[0].Expression
		if args[0].Kind == SQLText {
			at = "CASE WHEN pg_input_is_valid(" + at + ", 'timestamp with time zone') THEN (" + at + ")::timestamptz ELSE NULL::timestamptz END"
		} else if args[0].Kind != SQLTimestamp {
			return SQLColumn{}, errors.New("date-bucket requires text or a typed timestamp")
		}
		out.Expression, out.Kind = "to_char(date_trunc('"+unit+"', "+at+" AT TIME ZONE 'UTC'), 'YYYY-MM-DD\"T\"HH24:MI:SS.MS\"Z\"')", SQLText
	case "format-percent":
		value := sqlNumeric(args[0])
		out.Expression, out.Kind = "CASE WHEN "+value+" IS NULL THEN NULL::text ELSE trim_scale(round("+value+"*100,1))::text || '%' END", SQLText
	case "format-count":
		value := "round(" + sqlNumeric(args[0]) + ", 3)"
		whole := "split_part(abs(" + value + ")::text, '.', 1)"
		fraction := "rtrim(split_part(" + value + "::text, '.', 2), '0')"
		out.Expression, out.Kind = "CASE WHEN "+value+" IS NULL THEN NULL::text ELSE "+
			"CASE WHEN "+value+" < 0 THEN '-' ELSE '' END || regexp_replace("+whole+
			`, '([0-9])(?=([0-9]{3})+$)', '\1,', 'g') || CASE WHEN `+fraction+" <> '' THEN '.' || "+fraction+" ELSE '' END END", SQLText
	case "calendar-week-point", "failure-streak-point":
		if args[0].Kind != SQLTimestamp {
			return SQLColumn{}, errors.New("temporal point requires a typed timestamp")
		}
		at := "floor(extract(epoch FROM " + args[0].Expression + ") * 1000)"
		if computed.Function == "calendar-week-point" {
			reference := args[1].Expression
			if args[1].Kind != SQLTimestamp {
				value, ok := computed.Args[1].Value.(string)
				if !ok {
					return SQLColumn{}, errors.New("calendar reference requires a resolved timestamp")
				}
				_, err := time.Parse(time.RFC3339Nano, value)
				if err != nil {
					return SQLColumn{}, errors.New("calendar reference timestamp is invalid")
				}
				reference = "(" + args[1].Expression + ")::timestamptz"
			}
			success := sqlText(args[2]) + " = 'success'"
			if args[2].Kind == SQLBoolean {
				success = "COALESCE(" + args[2].Expression + ", FALSE)"
			}
			out.Expression, out.Kind, out.Point = "CASE WHEN "+args[0].Expression+" IS NOT NULL AND "+reference+" IS NOT NULL THEN "+
				"jsonb_build_array("+at+", floor(extract(epoch FROM "+reference+")*1000), "+success+")::text ELSE NULL::text END", SQLText, "calendar"
		} else {
			run := "trim(" + sqlText(args[1]) + ")"
			failed := "FALSE"
			switch args[2].Kind {
			case SQLBoolean:
				failed = "(" + args[2].Expression + ") IS TRUE"
			case SQLNumber, SQLText:
				failed = "coalesce(" + sqlNumeric(args[2]) + " > 0, FALSE)"
			default:
			}
			out.Expression, out.Kind, out.Point = "CASE WHEN "+args[0].Expression+" IS NOT NULL AND "+run+" <> '' THEN "+
				"jsonb_build_array("+at+", "+run+", "+failed+")::text ELSE NULL::text END", SQLText, "failure"
		}
	case "link-href":
		if args[0].Kind != SQLStructured {
			out.Expression, out.Kind = "NULL::text", SQLText
		} else {
			out.Expression, out.Kind = "nullif(("+args[0].Expression+"->>'href'), '')", SQLText
		}
	case "link":
		href, label := sqlText(args[0]), "trim("+sqlText(args[1])+")"
		out.Expression, out.Kind = "CASE WHEN "+href+" <> '' AND "+label+" <> '' THEN jsonb_build_object('href', "+href+", 'label', "+label+") ELSE NULL::jsonb END", SQLStructured
	case "dashboard-link":
		href, label := sqlText(args[1]), "trim("+sqlText(args[2])+")"
		identity := href
		if len(args) == 4 {
			identity = "trim(" + sqlText(args[3]) + ")"
		}
		existing := "'{}'::jsonb"
		if args[0].Kind == SQLStructured {
			existing = "CASE WHEN jsonb_typeof(" + args[0].Expression + ") = 'object' THEN " + args[0].Expression + " ELSE '{}'::jsonb END"
		}
		out.Expression, out.Kind = "CASE WHEN left("+href+",6) = '#page-' AND "+label+" <> '' AND "+identity+
			" <> '' THEN "+existing+" || jsonb_build_object('dashboard-href', "+href+", 'dashboard-label', "+label+") ELSE NULL::jsonb END", SQLStructured
	case "array-length":
		out.Expression, out.Kind = "NULL::numeric", SQLNumber
		if args[0].Kind == SQLStructured {
			out.Expression = "CASE WHEN jsonb_typeof(" + args[0].Expression + ") = 'array' THEN jsonb_array_length(" + args[0].Expression + ")::numeric ELSE NULL::numeric END"
		}
	default:
		return SQLColumn{}, fmt.Errorf("computed function %q has no SQL lowering", computed.Function)
	}
	// Literal arguments are bound values. A lowering that does not reference
	// one leaves an untyped parameter that Postgres rejects, so keep it typed.
	for index, argument := range args {
		if computed.Args[index].Field != nil || strings.Contains(out.Expression, argument.Expression) {
			continue
		}
		out.Expression = "(CASE WHEN (" + argument.Expression + " IS NOT DISTINCT FROM " + argument.Expression + ") THEN " +
			out.Expression + " END)"
	}
	return out, nil
}

func (c *sqlCompiler) union(inputs []SQLRelation, name string) (SQLRelation, error) {
	columns := map[string]SQLColumn{}
	for _, input := range inputs {
		for field, column := range input.Columns {
			if existing, exists := columns[field]; exists {
				kind, err := compatibleSQLKind(existing.Kind, column.Kind)
				if err != nil {
					return SQLRelation{}, err
				}
				column.Kind = kind
			}
			columns[field] = column
		}
	}
	fields := sortedSQLFields(columns)
	branches := make([]string, 0, len(inputs))
	for branch, input := range inputs {
		projection := []string{fmt.Sprintf("%d::bigint AS __branch", branch), input.Order + " AS __source_order"}
		for index, field := range fields {
			column, exists := input.Columns[field]
			if !exists {
				column = SQLColumn{Expression: "NULL::text", Kind: sqlNull, Presence: "FALSE"}
			}
			projection = append(projection, castSQLColumn(column, columns[field].Kind)+" AS "+SQLIdentifier(fmt.Sprintf("v%d", index)),
				column.Presence+" AS "+SQLIdentifier(fmt.Sprintf("p%d", index)))
		}
		branches = append(branches, "SELECT "+strings.Join(projection, ",")+" FROM "+input.SQL)
	}
	for index, field := range fields {
		column := columns[field]
		column.Expression, column.Presence = SQLIdentifier(fmt.Sprintf("v%d", index)), SQLIdentifier(fmt.Sprintf("p%d", index))
		columns[field] = column
	}
	return c.materialize(SQLRelation{SQL: "(" + strings.Join(branches, " UNION ALL ") + ") AS u",
		Columns: columns, Order: "row_number() OVER (ORDER BY __branch, __source_order)"}, name, "union", 0), nil
}

func (c *sqlCompiler) join(left, right SQLRelation, join Join, name string) (SQLRelation, error) {
	if right.SQL == "" {
		return SQLRelation{}, errors.New("joined relation is unavailable")
	}
	left, right = qualifyRelation(left, "l"), qualifyRelation(right, "r")
	var keys, condition, grouping []string
	for _, key := range join.On {
		a := sqlField(left, key.Left)
		b := sqlField(right, key.Right)
		if a.Kind == SQLStructured || b.Kind == SQLStructured {
			return SQLRelation{}, errors.New("structured join keys are forbidden")
		}
		leftKey, rightKey := "trim("+sqlText(a)+")", "trim("+sqlText(b)+")"
		condition = append(condition, leftKey+" <> '' AND "+rightKey+" <> '' AND "+leftKey+" = "+rightKey)
		keys = append(keys, rightKey+" <> ''")
		grouping = append(grouping, rightKey)
	}
	if join.Type != "inner-many" {
		c.joins = append(c.joins, "SELECT 1 FROM "+right.SQL+" WHERE "+strings.Join(keys, " AND ")+" GROUP BY "+
			strings.Join(grouping, ",")+" HAVING count(*) > 1 LIMIT 1")
	}
	joinType := "INNER JOIN"
	if join.Type == "left" {
		joinType = "LEFT JOIN"
	}
	columns := left.Columns
	for _, field := range join.Fields {
		column := sqlField(right, field.Field)
		alias := field.Field
		if field.As != "" {
			alias = field.As
		}
		if _, collision := columns[alias]; collision {
			return SQLRelation{}, errors.New("join field alias collides with an existing field")
		}
		column.Presence = "TRUE"
		columns[alias] = column
	}
	order := left.Order
	if join.Type == "inner-many" {
		order = "row_number() OVER (ORDER BY " + left.Order + "," + right.Order + ")"
	}
	return c.materialize(SQLRelation{SQL: left.SQL + " " + joinType + " " + right.SQL + " ON " + strings.Join(condition, " AND "),
		Columns: columns, Order: order}, name, "join", 1), nil
}

func (c *sqlCompiler) aggregate(input SQLRelation, aggregate *Aggregate, name string) (SQLRelation, error) {
	projection, groupBy := []string{}, []string{}
	columns := map[string]SQLColumn{}
	for index, field := range aggregate.By {
		column := sqlField(input, field)
		if column.Kind == SQLStructured {
			return SQLRelation{}, errors.New("structured grouping fields are forbidden")
		}
		valueName := "g" + strconv.Itoa(index)
		projection = append(projection, column.Expression+" AS "+SQLIdentifier(valueName),
			"bool_or("+column.Presence+") AS "+SQLIdentifier(valueName+"_present"))
		groupBy = append(groupBy, column.Expression)
		columns[field] = SQLColumn{Expression: SQLIdentifier(valueName), Presence: SQLIdentifier(valueName + "_present"), Kind: column.Kind}
	}
	for index, value := range aggregate.Values {
		column := sqlField(input, value.Field)
		if column.Kind == SQLStructured && value.Reducer != "unique" {
			return SQLRelation{}, errors.New("structured aggregate measures are forbidden")
		}
		predicate, err := c.filter(input, value.Filter)
		if err != nil {
			return SQLRelation{}, err
		}
		where := predicate + " AND (" + column.Expression + ") IS NOT NULL AND " + sqlText(column) + " <> ''"
		var expression string
		kind := SQLNumber
		switch value.Reducer {
		case "unique":
			kind = column.Kind
			first := "(array_agg(" + column.Expression + " ORDER BY " + input.Order + ") FILTER (WHERE " + predicate + " AND " + column.Expression + " IS NOT NULL))[1]"
			expression = "CASE WHEN count(DISTINCT to_jsonb(" + column.Expression + ")) FILTER (WHERE " + predicate + ") = 1 THEN " +
				first + " ELSE NULL END"
		case "count":
			expression = "count(*) FILTER (WHERE " + where + ")"
		case "distinct-count":
			expression = "count(DISTINCT " + sqlText(column) + ") FILTER (WHERE " + where + ")"
		case "distinct-list":
			expression, kind = "coalesce(string_agg(DISTINCT "+sqlText(column)+", ', ' ORDER BY "+sqlText(column)+") FILTER (WHERE "+where+"), '')", SQLText
		case "distinct-values":
			expression, kind = "coalesce(to_jsonb(array_agg(DISTINCT "+sqlText(column)+" ORDER BY "+sqlText(column)+") FILTER (WHERE "+where+")), '[]'::jsonb)", SQLStructured
		case "sum", "mean", "min", "max":
			function := value.Reducer
			if function == "mean" {
				function = "avg"
			}
			expression = function + "(" + sqlNumeric(column) + ") FILTER (WHERE " + where + ")"
			if value.Reducer == "sum" {
				expression = "coalesce(" + expression + ", 0::numeric)"
			}
		case "median", "p75", "p90", "p95":
			quantile := map[string]string{"median": "0.5", "p75": "0.75", "p90": "0.9", "p95": "0.95"}[value.Reducer]
			expression = "percentile_cont(" + quantile + ") WITHIN GROUP (ORDER BY (" + sqlNumeric(column) + ")::double precision) FILTER (WHERE " + where + ")"
		case "latest-failure-streak":
			if column.Point != "failure" {
				return SQLRelation{}, errors.New("latest-failure-streak requires failure-streak-point values")
			}
			points := "array_agg(" + column.Expression + " ORDER BY " + input.Order + ") FILTER (WHERE " + where + ")"
			expression = `(WITH points AS (
				SELECT (point::jsonb->>0)::numeric AS at, point::jsonb->>1 AS run,
					(point::jsonb->>2)::boolean AS failed
				FROM unnest(` + points + `) AS observations(point)),
				latest AS (SELECT DISTINCT ON (run) * FROM points ORDER BY run, at DESC),
				ordered AS (SELECT failed, row_number() OVER (ORDER BY at DESC, run DESC) AS position FROM latest)
				SELECT coalesce(min(position) FILTER (WHERE NOT failed)-1, count(*)) FROM ordered)`
		case "calendar-week-rhythm":
			if column.Point != "calendar" {
				return SQLRelation{}, errors.New("calendar-week-rhythm requires calendar-week-point values")
			}
			points := "array_agg(" + column.Expression + " ORDER BY " + input.Order + ") FILTER (WHERE " + where + ")"
			expression = `(WITH points AS (
				SELECT (point::jsonb->>0)::numeric AS at, (point::jsonb->>1)::numeric AS reference,
					(point::jsonb->>2)::boolean AS success, position
				FROM unnest(` + points + `) WITH ORDINALITY AS observations(point, position)),
				reference AS (SELECT (to_timestamp(reference/1000) AT TIME ZONE 'UTC')::date AS day
					FROM points ORDER BY position LIMIT 1),
				days AS (SELECT reference.day, (date_trunc('week', reference.day)::date + slot) AS date, slot
					FROM reference CROSS JOIN generate_series(0,6) AS slots(slot)),
				counts AS (SELECT (to_timestamp(at/1000) AT TIME ZONE 'UTC')::date AS date, count(*) AS count
					FROM points WHERE success GROUP BY 1)
				SELECT jsonb_build_object('days', jsonb_agg(jsonb_build_object(
					'label', (ARRAY['Mon','Tue','Wed','Thu','Fri','Sat','Sun'])[days.slot+1],
					'date', to_char(days.date, 'YYYY-MM-DD'),
					'current', coalesce(current.count,0),
					'previous', coalesce(previous.count,0),
					'reached', days.date <= days.day) ORDER BY days.slot))
				FROM days LEFT JOIN counts AS current ON current.date = days.date
				LEFT JOIN counts AS previous ON previous.date = days.date-7)`
			kind = SQLStructured
		default:
			return SQLRelation{}, fmt.Errorf("reducer %q has no SQL lowering", value.Reducer)
		}
		alias := "a" + strconv.Itoa(index)
		projection = append(projection, expression+" AS "+SQLIdentifier(alias))
		if _, collision := columns[value.As]; collision {
			return SQLRelation{}, errors.New("aggregate alias collides with a group field")
		}
		columns[value.As] = SQLColumn{Expression: SQLIdentifier(alias), Presence: "TRUE", Kind: kind}
	}
	sql := "SELECT " + strings.Join(projection, ",") + " FROM " + input.SQL
	if len(groupBy) != 0 {
		sql += " GROUP BY " + strings.Join(groupBy, ",")
	}
	order := "1::bigint"
	if len(aggregate.By) != 0 {
		parts := make([]string, 0, len(aggregate.By))
		for _, field := range aggregate.By {
			parts = append(parts, columns[field].Expression+" NULLS FIRST")
		}
		order = "row_number() OVER (ORDER BY " + strings.Join(parts, ",") + ")"
	}
	return c.materialize(SQLRelation{SQL: "(" + sql + ") AS a", Columns: columns, Order: order}, name, "aggregate", len(aggregate.Values)), nil
}
