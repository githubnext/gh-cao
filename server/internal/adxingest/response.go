package adxingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

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
	roles := map[int]string{}
	var toc *responseTable
	names := map[string]bool{}
	for i, table := range response.Tables {
		if table.TableName == "" || names[table.TableName] {
			return nil, errors.New("ambiguous ADX response table")
		}
		names[table.TableName] = true
		columns := map[string]bool{}
		for _, column := range table.Columns {
			if columns[column.ColumnName] {
				return nil, errors.New("ambiguous ADX response column")
			}
			columns[column.ColumnName] = true
		}
		if table.column("Ordinal") >= 0 && table.column("Kind") >= 0 && table.column("Name") >= 0 {
			if toc != nil {
				return nil, errors.New("ambiguous ADX table of contents")
			}
			toc = &response.Tables[i]
		}
	}
	if toc != nil {
		seen := map[int64]bool{}
		for _, row := range toc.Rows {
			if len(row) != len(toc.Columns) {
				return nil, errors.New("invalid ADX table of contents row")
			}
			number, ok := row[toc.column("Ordinal")].(json.Number)
			if !ok {
				return nil, errors.New("invalid ADX table ordinal")
			}
			ordinal, err := number.Int64()
			if err != nil || ordinal < 0 || ordinal >= int64(len(response.Tables)) || seen[ordinal] {
				return nil, errors.New("invalid or duplicate ADX table ordinal")
			}
			seen[ordinal] = true
			kind, _ := row[toc.column("Kind")].(string)
			name, _ := row[toc.column("Name")].(string)
			switch kind {
			case "QueryResult":
				if name != "PrimaryResult" {
					return nil, errors.New("unexpected ADX result table")
				}
				roles[int(ordinal)] = "result"
			case "QueryStatus":
				if name != "QueryStatus" {
					return nil, errors.New("invalid ADX status mapping")
				}
				roles[int(ordinal)] = "status"
			}
		}
	} else {
		for i, table := range response.Tables {
			switch table.TableName {
			case "QueryStatus":
				roles[i] = "status"
			case "PrimaryResult", "Table_0":
				roles[i] = "result"
			default:
				if table.column("Severity") >= 0 {
					return nil, errors.New("unmapped ADX query status")
				}
			}
		}
	}
	found, status := false, false
	var result [][]any
	for i, table := range response.Tables {
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
	if !found || len(result) > 2048 || toc != nil && !status {
		return nil, errors.New("missing or oversized ADX query result/status")
	}
	return result, nil
}

func checkQueryStatus(table responseTable) error {
	severity, code := table.column("Severity"), table.column("StatusCode")
	if severity < 0 {
		return errors.New("invalid ADX query status")
	}
	for _, row := range table.Rows {
		if severity >= len(row) || code >= len(row) {
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
