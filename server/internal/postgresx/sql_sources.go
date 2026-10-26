package postgresx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func sqlKind(column entityColumn) query.SQLKind {
	switch column.kind {
	case "numeric":
		return query.SQLNumber
	case "boolean":
		return query.SQLBoolean
	case "timestamp":
		return query.SQLTimestamp
	case "object":
		return query.SQLStructured
	default:
		return query.SQLText
	}
}

func relationColumns(table entityTable, presence func(int) string) map[string]query.SQLColumn {
	columns := make(map[string]query.SQLColumn, len(table.columns))
	for index, column := range table.columns {
		if strings.Contains(column.field, ".") {
			continue
		}
		expression := "t." + query.SQLIdentifier(column.name)
		kind := sqlKind(column)
		if column.kind == "object" {
			var parts []string
			for memberIndex, member := range table.columns {
				prefix := column.field + "."
				if !strings.HasPrefix(member.field, prefix) {
					continue
				}
				field := strings.TrimPrefix(member.field, prefix)
				parts = append(parts, "CASE WHEN "+presence(memberIndex)+" THEN jsonb_build_object('"+
					strings.ReplaceAll(field, "'", "''")+"', t."+query.SQLIdentifier(member.name)+") ELSE '{}'::jsonb END")
			}
			expression = "CASE WHEN " + expression + " IS TRUE THEN (" + strings.Join(parts, " || ") + ") ELSE NULL::jsonb END"
		}
		columns[column.field] = query.SQLColumn{Expression: expression, Presence: presence(index), Kind: kind}
	}
	return columns
}

func (r *readTransaction) entitySQLSource(name string) (query.SQLRelation, error) {
	if name == dailySource || name == "$dailyRunCounts" {
		return dailySQLSource(r.store.namespace, r.store.dailyCounts, name == "$dailyRunCounts"), nil
	}
	if name == "$operationalValueEvidence" {
		return r.operationalValueEvidenceSQLSource(), nil
	}
	table, exists := entityTables[name]
	if !exists || table.runtime || table.internal {
		return query.SQLRelation{}, fmt.Errorf("SQL source %q is not registered by TypeSpec", name)
	}

	columns := relationColumns(table, func(index int) string { return "get_bit(t.present_fields, " + strconv.Itoa(index) + ") = 1" })
	if name == "$operationalValues" {
		route := "CASE WHEN t.workflow_slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND starts_with(t.value_id,t.workflow_slug||'.') THEN t.repository||':.github/workflows/'||t.workflow_slug||'.md' END"
		columns["workflow-route"] = query.SQLColumn{Expression: route, Presence: "(" + route + ") IS NOT NULL", Kind: query.SQLText}
	}
	return query.SQLRelation{
		SQL:     "(SELECT * FROM " + query.SQLIdentifier(table.name) + " WHERE namespace = {}) AS t",
		Params:  []any{r.store.namespace},
		Columns: columns,
		Order:   "t.ordinal",
	}, nil
}

// The logical input is a projection of typed child facts, not a saved report.
func (r *readTransaction) operationalValueEvidenceSQLSource(selections ...query.SourceEquality) query.SQLRelation {
	facts := entityTables["$operationalValueEvidence.facts"]
	columns := relationColumns(facts, func(index int) string {
		return "get_bit(t.present_fields, " + strconv.Itoa(index) + ") = 1"
	})
	delete(columns, "id")
	delete(columns, "packetId")
	projections := make([]string, 1, 10)
	projections[0] = "f.*"
	for logical, physical := range map[string]string{
		"report-id": "id", "workflow": "workflow", "workflow-path": "source_path", "observed-at": "observed_at",
		"window-start": "window_start", "window-end": "window_end", "contract-digest": "contract_digest",
	} {
		kind := query.SQLText
		if logical == "observed-at" || logical == "window-start" || logical == "window-end" {
			kind = query.SQLTimestamp
		}
		alias := "__" + physical
		projections = append(projections, "p."+query.SQLIdentifier(physical)+" AS "+query.SQLIdentifier(alias))
		columns[logical] = query.SQLColumn{Expression: "t." + query.SQLIdentifier(alias), Presence: "TRUE", Kind: kind}
	}
	projections = append(projections, "split_part(p.repository, '/', 1) AS __organization", "split_part(p.repository, '/', 2) AS __repository")
	columns["organization"] = query.SQLColumn{Expression: "t.__organization", Presence: "TRUE", Kind: query.SQLText}
	columns["repository"] = query.SQLColumn{Expression: "t.__repository", Presence: "TRUE", Kind: query.SQLText}
	projections = append(projections, "p.repository || ':' || p.source_path AS __workflow_route")
	columns["workflow-route"] = query.SQLColumn{Expression: "t.__workflow_route", Presence: "TRUE", Kind: query.SQLText}
	projections = append(projections, `CASE f.fact_kind WHEN 'snapshot' THEN p.id || '|' || f.fact_id
		WHEN 'snapshot-item' THEN p.id || '|' || f.parent_id END AS __snapshot_route`)
	columns["snapshot-route"] = query.SQLColumn{Expression: "t.__snapshot_route", Presence: "t.__snapshot_route IS NOT NULL", Kind: query.SQLText}
	predicate, params := r.operationalValueEvidencePredicate(selections)
	return query.SQLRelation{
		SQL:    "(SELECT " + strings.Join(projections, ",") + " FROM operational_value_evidence_facts f JOIN operational_value_evidence p ON p.namespace=f.namespace AND p.id=f.packet_id WHERE " + predicate + ") AS t",
		Params: params, Columns: columns, Order: "t.ordinal",
	}
}

func (r *readTransaction) operationalValueEvidencePredicate(selections []query.SourceEquality) (string, []any) {
	predicate := "p.namespace={}"
	params := []any{r.store.namespace}
	var scopes []string
	for _, selection := range selections {
		switch selection.Field {
		case "report-id":
			scopes = append(scopes, "p.id={}")
			params = append(params, selection.Value)
		case "workflow-route":
			scopes = append(scopes, "p.workflow_id={}")
			params = append(params, "workflow:"+model.EncodeCoordinate(strings.ToLower(selection.Value)))
		}
	}
	if len(scopes) > 0 {
		predicate += " AND (" + strings.Join(scopes, " OR ") + ")"
	}
	return predicate, params
}

func runtimeSQLSource(name string, source model.Source) (query.SQLRelation, error) {
	table, exists := entityTables[name]
	if !exists || !table.runtime {
		return query.SQLRelation{}, errors.New("runtime SQL source is not explicitly registered")
	}
	if len(source.Rows) > query.MaxInputRows {
		return query.SQLRelation{}, fmt.Errorf("query %q exceeds max input rows", name)
	}
	if query.EstimateRowsBytes(source.Rows) > query.MaxWorkingBytes {
		return query.SQLRelation{}, fmt.Errorf("query %q exceeds max working bytes", name)
	}
	aliases := []string{}
	for index, column := range table.columns {
		aliases = append(aliases, query.SQLIdentifier(column.name), query.SQLIdentifier(fmt.Sprintf("__p_%d", index)))
	}
	columns := relationColumns(table, func(index int) string { return "t." + query.SQLIdentifier(fmt.Sprintf("__p_%d", index)) })
	workingBytes := query.EstimateRowsBytes(source.Rows) + int64(len(source.Rows))*int64(len(table.columns))*32
	if workingBytes > query.MaxWorkingBytes {
		return query.SQLRelation{}, fmt.Errorf("query %q exceeds max working bytes", name)
	}
	var parameters []string
	args := make([]any, 0, len(table.columns)*2)
	for _, column := range table.columns {
		values := make([]any, len(source.Rows))
		presence := make([]bool, len(source.Rows))
		for index, row := range source.Rows {
			value, present := entityFieldValue(row, column.field)
			bound, err := column.bind(value)
			if err != nil {
				return query.SQLRelation{}, fmt.Errorf("runtime %s.%s: %w", name, column.field, err)
			}
			values[index], presence[index] = bound, present
		}
		args = append(args, values, presence)
		parameters = append(parameters, "{}::"+column.sql+"[]", "{}::boolean[]")
	}
	aliases = append(aliases, "ordinal")
	relation := "unnest(" + strings.Join(parameters, ",") + ") WITH ORDINALITY AS t(" + strings.Join(aliases, ",") + ")"
	return query.SQLRelation{SQL: relation, Params: args, Columns: columns, Order: "t.ordinal"}, nil
}
