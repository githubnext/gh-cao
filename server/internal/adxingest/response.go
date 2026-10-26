package adxingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// maxQueryResultRows bounds the rows accepted from one ADX query result.
const maxQueryResultRows = 2048

type responseTable struct {
	TableName string
	Rows      [][]any
	Columns   []struct{ ColumnName string }
}

func (t responseTable) column(name string) int {
	for i, column := range t.Columns {
		if column.ColumnName == name {
			return i
		}
	}
	return -1
}

func decodeQueryResponse(content []byte) ([][]any, error) {
	var response struct {
		Tables     []responseTable
		Error      json.RawMessage `json:"error"`
		Exceptions []json.RawMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil || len(response.Error) > 0 || len(response.Exceptions) > 0 {
		return nil, errors.New("invalid ADX query response")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing ADX query response")
	}
	roles, toc, err := classifyTables(response.Tables)
	if err != nil {
		return nil, err
	}
	return selectQueryResult(response.Tables, roles, toc)
}

// selectQueryResult picks the single primary result table, validating its
// status table when one is present, and enforces the row cap.
func selectQueryResult(tables []responseTable, roles map[int]string, toc *responseTable) ([][]any, error) {
	found, status := false, false
	var result [][]any
	for i, table := range tables {
		switch roles[i] {
		case "status":
			if status {
				return nil, errors.New("ambiguous ADX query status")
			}
			status = true
			if err := checkQueryStatus(table); err != nil {
				return nil, err
			}
		case "result":
			if found {
				return nil, errors.New("ambiguous ADX query result")
			}
			found, result = true, table.Rows
		default:
			if table.column("Severity") >= 0 {
				return nil, errors.New("unmapped ADX query status")
			}
		}
	}
	if !found || len(result) > maxQueryResultRows || toc != nil && !status {
		exportLog.Printf("ADX query result rejected: tables=%d found=%t status=%t rows=%d", len(tables), found, status, len(result))
		return nil, errors.New("missing or oversized ADX query result/status")
	}
	exportLog.Printf("ADX query result selected: tables=%d rows=%d", len(tables), len(result))
	return result, nil
}

// classifyTables maps each response table to a "result" or "status" role,
// using the table of contents when present and table names otherwise.
func classifyTables(tables []responseTable) (map[int]string, *responseTable, error) {
	roles := map[int]string{}
	var toc *responseTable
	names := map[string]bool{}
	for i, table := range tables {
		if table.TableName == "" || names[table.TableName] {
			return nil, nil, errors.New("ambiguous ADX response table")
		}
		names[table.TableName] = true
		columns := map[string]bool{}
		for _, column := range table.Columns {
			if columns[column.ColumnName] {
				return nil, nil, errors.New("ambiguous ADX response column")
			}
			columns[column.ColumnName] = true
		}
		if table.column("Ordinal") >= 0 && table.column("Kind") >= 0 && table.column("Name") >= 0 {
			if toc != nil {
				return nil, nil, errors.New("ambiguous ADX table of contents")
			}
			toc = &tables[i]
		}
	}
	if toc != nil {
		seen := map[int64]bool{}
		for _, row := range toc.Rows {
			if len(row) != len(toc.Columns) {
				return nil, nil, errors.New("invalid ADX table of contents row")
			}
			number, ok := row[toc.column("Ordinal")].(json.Number)
			if !ok {
				return nil, nil, errors.New("invalid ADX table ordinal")
			}
			ordinal, err := number.Int64()
			if err != nil || ordinal < 0 || ordinal >= int64(len(tables)) || seen[ordinal] {
				return nil, nil, errors.New("invalid or duplicate ADX table ordinal")
			}
			seen[ordinal] = true
			kind, _ := row[toc.column("Kind")].(string)
			name, _ := row[toc.column("Name")].(string)
			switch kind {
			case "QueryResult":
				if name != "PrimaryResult" {
					return nil, nil, errors.New("unexpected ADX result table")
				}
				roles[int(ordinal)] = "result"
			case "QueryStatus":
				if name != "QueryStatus" {
					return nil, nil, errors.New("invalid ADX status mapping")
				}
				roles[int(ordinal)] = "status"
			}
		}
	} else {
		for i, table := range tables {
			switch table.TableName {
			case "QueryStatus":
				roles[i] = "status"
			case "PrimaryResult", "Table_0":
				roles[i] = "result"
			default:
				if table.column("Severity") >= 0 {
					return nil, nil, errors.New("unmapped ADX query status")
				}
			}
		}
	}
	return roles, toc, nil
}

func checkQueryStatus(table responseTable) error {
	severity, code := table.column("Severity"), table.column("StatusCode")
	if severity < 0 || len(table.Rows) == 0 {
		return errors.New("invalid ADX query status")
	}
	for _, row := range table.Rows {
		if len(row) != len(table.Columns) || severity >= len(row) || code >= len(row) {
			return errors.New("invalid ADX query status row")
		}
		value, ok := row[severity].(json.Number)
		if !ok {
			return errors.New("invalid ADX query severity")
		}
		n, err := value.Int64()
		if err != nil || n < 0 || n > 6 {
			return errors.New("invalid ADX query severity")
		}
		if n <= 2 {
			return errors.New("ADX query reported partial execution")
		}
		if code >= 0 {
			value, ok := row[code].(json.Number)
			if !ok {
				return errors.New("invalid ADX query status code")
			}
			n, err := value.Int64()
			if err != nil || n != 0 {
				return errors.New("ADX query reported partial execution")
			}
		}
	}
	return nil
}
