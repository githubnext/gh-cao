package adxingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

var projectionLog = logger.New("cao:adx:projection")

type field struct{ source, kind string }
type family struct {
	table, source string
	fields        []field
}

type Projection struct {
	Namespace    string
	RunID        string
	RepositoryID string
	Revision     int64
	ID           string
	Tables       map[string][]model.Row
	Manifest     model.Row
}

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func project(session sessionexport.Session) (Projection, error) {
	runID, ok := session.Run["id"].(string)
	repositoryID, repositoryOK := session.Run["repositoryId"].(string)
	if !ok || runID == "" || !repositoryOK || repositoryID == "" || session.Namespace == "" || session.Revision <= 0 {
		return Projection{}, errors.New("ADX requires a published canonical run identity")
	}
	projection := Projection{Namespace: session.Namespace, RunID: runID, RepositoryID: repositoryID,
		Revision: session.Revision, Tables: map[string][]model.Row{}}
	var digests []string
	rowCount := 0
	for _, family := range families {
		projection.Tables[family.table] = nil
		input := session.Facts[family.source]
		if family.source == "$runs" {
			input = []model.Row{session.Run}
		}
		seen := map[string]bool{}
		for _, canonical := range input {
			id, valid := canonical["id"].(string)
			if !valid || id == "" || seen[id] {
				return Projection{}, errors.New("ADX observation identity is missing or duplicated")
			}
			if family.source != "$runs" && canonical["runId"] != runID {
				return Projection{}, errors.New("ADX observation belongs to another run")
			}
			seen[id] = true
			row, err := observationRow(session, runID, family, id, canonical)
			if err != nil {
				return Projection{}, err
			}
			content, err := json.Marshal(row)
			if err != nil {
				return Projection{}, err
			}
			row["RowDigest"] = hash(content)
			digests = append(digests, family.table+":"+row["RowDigest"].(string))
			projection.Tables[family.table] = append(projection.Tables[family.table], row)
			rowCount++
			if rowCount > sessionexport.MaxRows {
				projectionLog.Printf("projection rejected: reason=too_many_rows limit=%d", sessionexport.MaxRows)
				return Projection{}, sessionexport.ErrTooLarge
			}
		}
	}
	slices.Sort(digests)
	projection.ID = hash([]byte(strings.Join(digests, "\n")))
	for table, rows := range projection.Tables {
		slices.SortFunc(rows, func(a, b model.Row) int { return strings.Compare(a["RowDigest"].(string), b["RowDigest"].(string)) })
		for _, row := range rows {
			row["ProjectionId"] = projection.ID
		}
		projection.Tables[table] = rows
	}
	projection.Manifest = model.Row{
		"Namespace": session.Namespace, "RunId": runID, "ProjectionId": projection.ID,
		"SourceRevision": session.Revision, "ContractVersion": contractVersion,
		"Coverage": "retained-canonical-snapshot", "UsageObservations": "unavailable",
	}
	for _, family := range families {
		projection.Manifest[family.table+"Count"] = len(projection.Tables[family.table])
	}
	if value, exists := session.Run["attempt"]; exists {
		projection.Manifest["RunAttempt"] = value
	}
	content, err := json.Marshal(projection.Manifest)
	if err != nil {
		return Projection{}, err
	}
	projection.Manifest["RowDigest"] = hash(content)
	content, err = json.Marshal(projection)
	if err != nil {
		return Projection{}, err
	}
	if len(content) > sessionexport.MaxBytes {
		projectionLog.Printf("projection rejected: reason=too_large bytes=%d limit=%d", len(content), sessionexport.MaxBytes)
		return Projection{}, sessionexport.ErrTooLarge
	}
	projectionLog.Printf("projected session: tables=%d rows=%d bytes=%d", len(projection.Tables), rowCount, len(content))
	return projection, nil
}

// observationRow maps one canonical observation to its allowlisted ADX row,
// recording which fields were present or explicitly null.
func observationRow(session sessionexport.Session, runID string, family family, id string, canonical model.Row) (model.Row, error) {
	row := model.Row{"Namespace": session.Namespace, "RunId": runID, "ObservationId": id, "ContractVersion": contractVersion}
	presence := make([]string, 0, len(family.fields)+1)
	nulls := make([]string, 0)
	if attempt, present := session.Run["attempt"]; present {
		if !validScalar(attempt, "long") {
			return nil, errors.New("invalid canonical run attempt")
		}
		row["RunAttempt"] = attempt
		presence = append(presence, "RunAttempt")
		if attempt == nil {
			nulls = append(nulls, "RunAttempt")
		}
	}
	for _, field := range family.fields {
		if value, present := canonical[field.source]; present {
			if !validScalar(value, field.kind) {
				return nil, fmt.Errorf("invalid canonical ADX field %s", field.source)
			}
			name := columnName(field.source)
			row[name] = value
			presence = append(presence, name)
			if value == nil {
				nulls = append(nulls, name)
			}
		}
	}
	row["PresentFields"] = presence
	row["NullFields"] = nulls
	quality := session.Quality[family.source]
	for _, key := range []string{"availability", "completeness", "freshness"} {
		value, ok := quality[key].(string)
		if !ok {
			value = "unknown"
		}
		row[columnName(key)] = value
	}
	return row, nil
}

func columnName(field string) string { return strings.ToUpper(field[:1]) + field[1:] }

func validScalar(value any, kind string) bool {
	if value == nil {
		return true
	}
	switch kind {
	case "string":
		text, ok := value.(string)
		return ok && len(text) <= 1024
	case "datetime":
		if _, ok := value.(time.Time); ok {
			return true
		}
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, text)
		return err == nil
	case "bool":
		_, ok := value.(bool)
		return ok
	default:
		switch value.(type) {
		case json.Number, float64, float32, int, int64, int32:
		default:
			return false
		}
		content, err := json.Marshal(value)
		if err != nil {
			return false
		}
		var number json.Number
		if err := json.Unmarshal(content, &number); err != nil {
			return false
		}
		if kind == "long" {
			_, err = number.Int64()
		} else {
			_, err = number.Float64()
		}
		return err == nil
	}
}
