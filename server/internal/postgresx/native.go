package postgresx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

type entityColumn struct{ field, name, kind, sql string }
type entityChild struct{ field, source string }
type entityTable struct {
	name        string
	runtime     bool
	canonical   bool
	partitioned bool
	internal    bool
	columns     []entityColumn
	children    []entityChild
}

func IsCanonicalCollection(name string) bool {
	table, exists := entityTables["$"+name]
	return exists && table.canonical
}

func entityFieldValue(row model.Row, path string) (any, bool) {
	parts := strings.Split(path, ".")
	value, present := row[parts[0]]
	if len(parts) == 1 {
		return value, present
	}
	if text, ok := value.(string); ok && (strings.HasSuffix(parts[0], "Link") || strings.HasSuffix(parts[0], "-link")) {
		switch parts[1] {
		case "href":
			return text, present
		case "relation":
			return strings.TrimSuffix(strings.TrimSuffix(parts[0], "Link"), "-link"), present
		default:
			return nil, false
		}
	}
	if value, ok := value.(map[string]any); ok {
		field, exists := value[parts[1]]
		return field, exists
	}
	if value, ok := value.(model.Row); ok {
		field, exists := value[parts[1]]
		return field, exists
	}
	return nil, false
}

func (column entityColumn) bind(value any) (any, error) {
	switch pointer := value.(type) {
	case *int:
		if pointer == nil {
			return nil, nil
		}
		value = *pointer
	case *int64:
		if pointer == nil {
			return nil, nil
		}
		value = *pointer
	case *float64:
		if pointer == nil {
			return nil, nil
		}
		value = *pointer
	}
	if value == nil {
		return nil, nil
	}
	switch column.kind {
	case "object":
		switch value.(type) {
		case model.Row, map[string]any, string:
			return true, nil
		}
	case "boolean":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
	case "numeric":
		var text string
		switch number := value.(type) {
		case json.Number:
			text = string(number)
		case float64, float32, int, int64, int32, uint64:
			text = fmt.Sprint(number)
		default:
			return nil, errors.New("expected a native number")
		}
		if column.sql == "BIGINT" {
			return strconv.ParseInt(text, 10, 64)
		}
		number := pgtype.Numeric{}
		if err := number.Scan(text); err != nil {
			return nil, errors.New("invalid finite numeric field")
		}
		if number.NaN || number.InfinityModifier != pgtype.Finite {
			return nil, errors.New("numeric field must be finite")
		}
		return number, nil
	case "timestamp":
		if instant, ok := value.(time.Time); ok {
			return instant, nil
		}
		if text, ok := value.(string); ok {
			instant, valid := auditTimestamp(text)
			if !valid {
				return nil, errors.New("invalid canonical timestamp")
			}
			return instant, nil
		}
	default:
		if text, ok := value.(string); ok {
			return text, nil
		}
	}
	return nil, errors.New("invalid native field type")
}

const BatchRows = 512
const BatchBytes = 8 << 20

// Writer replaces one namespace under a transaction-scoped writer lock.
// Rows are bounded per batch and are never retained beyond CopyFrom.
type Writer struct {
	store                *Store
	connection           *pgx.Conn
	tx                   pgx.Tx
	ordinals             map[string]int64
	inventoryOrdinals    map[string]int64
	inventoryTables      map[string]bool
	batches              map[string][][]any
	rows, bytes          int
	revision             int64
	PreviousDataRevision string
	evaluatedAt          time.Time
	ingestedAt           time.Time
	staged               map[string]bool
	preserveValues       bool
}

func (s *Store) BeginIngestion(ctx context.Context) (*Writer, error) {
	connection, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return nil, err
	}
	tx, err := connection.Begin(ctx)
	if err != nil {
		_ = connection.Close(ctx)
		return nil, err
	}
	writer := &Writer{store: s, connection: connection, tx: tx, ordinals: map[string]int64{}, inventoryOrdinals: map[string]int64{}, inventoryTables: map[string]bool{}, batches: map[string][][]any{}, evaluatedAt: time.Unix(0, 0).UTC(), ingestedAt: time.Now().UTC(), staged: map[string]bool{}}
	fail := func(err error) (*Writer, error) { writer.Abort(ctx); return nil, err }
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(712083241, 17484)`); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`, s.namespace); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cao_state (namespace, revision, data_revision, evaluated_at, schema_version)
		VALUES ($1,0,'','epoch',$2) ON CONFLICT (namespace) DO NOTHING`, s.namespace, model.SchemaVersion); err != nil {
		return fail(err)
	}
	if err := tx.QueryRow(ctx, `SELECT revision,data_revision FROM cao_state WHERE namespace=$1 FOR UPDATE`, s.namespace).Scan(&writer.revision, &writer.PreviousDataRevision); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		return fail(err)
	}
	if err := writer.preserveValuePublications(ctx); err != nil {
		return fail(err)
	}
	if err := writer.preserveCollectionDecisions(ctx); err != nil {
		return fail(err)
	}
	writer.preserveValues = true
	if err := writer.stageAdmittedRuns(ctx); err != nil {
		return fail(err)
	}
	for _, source := range tableNames() {
		statement, args, err := sqlbuilder.Build("DELETE FROM {} WHERE namespace={}", sqlbuilder.Identifier(entityTables[source].name), s.namespace)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, statement, args...); err != nil {
			return fail(err)
		}
	}
	if _, err := tx.Exec(ctx, "DELETE FROM cao_quality WHERE namespace=$1", s.namespace); err != nil {
		return fail(err)
	}
	return writer, nil
}

func (w *Writer) Abort(ctx context.Context) {
	if w.tx != nil {
		_ = w.tx.Rollback(context.WithoutCancel(ctx))
		w.tx = nil
	}
	if w.connection != nil {
		_ = w.connection.Close(context.WithoutCancel(ctx))
		w.connection = nil
	}
}

func (w *Writer) Append(ctx context.Context, source string, row model.Row) error {
	return w.append(ctx, source, row, false)
}

func (w *Writer) AppendInventory(ctx context.Context, source string, row model.Row) error {
	if !w.inventoryTables[source] {
		table, exists := entityTables[source]
		if !exists || table.runtime {
			return fmt.Errorf("unregistered inventory collection %q", source)
		}
		statement, args, err := sqlbuilder.Build("CREATE TEMP TABLE {} (LIKE {} INCLUDING ALL) ON COMMIT DROP",
			sqlbuilder.Identifier(table.name+"_inventory"), sqlbuilder.Identifier(table.name))
		if err != nil {
			return err
		}
		if _, err := w.tx.Exec(ctx, statement, args...); err != nil {
			return err
		}
		w.inventoryTables[source] = true
	}
	return w.append(ctx, source, row, true)
}

func (w *Writer) append(ctx context.Context, source string, row model.Row, inventory bool) error {
	if source == "$workflowValueCollections" {
		if err := validateWorkflowValueCollection(row); err != nil {
			return err
		}
	}
	if source == "$operationalValueEvidence" {
		if w.preserveValues {
			var previous time.Time
			err := w.tx.QueryRow(ctx, `SELECT e.observed_at FROM cao_value_publications p
				LEFT JOIN operational_value_evidence_value_preserved e ON e.namespace=p.namespace AND e.id=p.packet_id
				WHERE p.namespace=$1 AND p.packet_id=$2`,
				w.store.namespace, evidenceText(row["id"])).Scan(&previous)
			if err == nil {
				return validateValueSnapshotCapture(previous, row)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if err := validateOperationalValueEvidence(row); err != nil {
			return err
		}
	}
	table, exists := entityTables[source]
	if !exists || table.runtime {
		return fmt.Errorf("canonical collection %q is not registered by TypeSpec", source)
	}
	row, err := normalizeProjectedFacts(source, row)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	id, valid := row["id"].(string)
	if !valid || id == "" {
		return errors.New("native row requires a nonempty string id")
	}
	if source == "$audits" {
		if err := validateAuditProjection(row); err != nil {
			return err
		}
	}
	if err := validateAuditReferenceProjection(source, row); err != nil {
		return err
	}
	present := make([]byte, len(table.columns))
	values := make([]any, len(table.columns)+3)
	if table.partitioned && table.name == "runs" {
		values = append(values, w.ingestedAt)
		for _, field := range []string{"createdAt", "startedAt", "completedAt", "updatedAt"} {
			if value, ok := row[field]; ok && value != nil {
				bound, err := (entityColumn{kind: "timestamp"}).bind(value)
				if err != nil {
					return fmt.Errorf("%s.%s: %w", source, field, err)
				}
				values[len(values)-1] = bound
				break
			}
		}
	}
	rowBytes := 0
	ordinals := w.ordinals
	batchName := source
	if inventory {
		ordinals = w.inventoryOrdinals
		batchName = source + "#inventory"
	}
	values[0], values[1] = w.store.namespace, ordinals[source]
	for index, column := range table.columns {
		value, exists := entityFieldValue(row, column.field)
		present[index] = '0'
		if exists {
			present[index] = '1'
		}
		bound, err := column.bind(value)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", source, column.field, err)
		}
		values[index+3] = bound
		switch value := bound.(type) {
		case string:
			rowBytes += len(value)
		case time.Time:
			rowBytes += 16
		default:
			rowBytes += 32
		}
	}
	if rowBytes > BatchBytes {
		return errors.New("native record exceeds the bounded ingestion row budget")
	}
	if w.bytes+rowBytes > BatchBytes {
		if err := w.Flush(ctx); err != nil {
			return err
		}
	}
	w.bytes += rowBytes
	// #nosec G115 -- the column count is bounded by the generated TypeSpec registry.
	values[2] = pgtype.Bits{Bytes: presenceBytes(present), Len: int32(len(present)), Valid: true}
	ordinals[source]++
	w.batches[batchName] = append(w.batches[batchName], values)
	w.rows++
	for _, child := range table.children {
		items, ok := row[child.field].([]any)
		if !ok || len(items) > 20000 {
			return errors.New("typed evidence facts must be a bounded native array")
		}
		for index, item := range items {
			fact, ok := item.(map[string]any)
			if !ok {
				return errors.New("typed evidence fact must be an object")
			}
			owned := model.Row{"id": fmt.Sprintf("%s:%d", id, index), "packetId": id}
			for field, value := range fact {
				owned[field] = value
			}
			if err := w.append(ctx, child.source, owned, false); err != nil {
				return err
			}
		}
	}
	if w.rows >= BatchRows || w.bytes >= BatchBytes {
		return w.Flush(ctx)
	}
	return nil
}

func presenceBytes(present []byte) []byte {
	value := make([]byte, (len(present)+7)/8)
	for index, bit := range present {
		if bit == '1' {
			value[index/8] |= 1 << (7 - index%8)
		}
	}
	return value
}

func (w *Writer) Flush(ctx context.Context) error {
	for source, batch := range w.batches {
		if len(batch) == 0 {
			continue
		}
		sourceName := strings.TrimSuffix(source, "#inventory")
		table := entityTables[sourceName]
		tableName := table.name
		if sourceName != source {
			tableName += "_inventory"
		} else if table.partitioned && table.name != "runs" {
			tableName += "_stage"
			if !w.staged[sourceName] {
				if _, err := w.tx.Exec(ctx, "CREATE TEMP TABLE "+query.SQLIdentifier(tableName)+" ON COMMIT DROP AS SELECT * FROM "+query.SQLIdentifier(table.name)+" WITH NO DATA"); err != nil {
					return err
				}
				w.staged[sourceName] = true
			}
		}
		columns := make([]string, 0, 3+len(table.columns))
		columns = append(columns, "namespace", "ordinal", "present_fields")
		for _, column := range table.columns {
			columns = append(columns, column.name)
		}
		if table.name == "runs" && sourceName == source {
			columns = append(columns, "run_at")
		}
		if _, err := w.tx.CopyFrom(ctx, pgx.Identifier{tableName}, columns, pgx.CopyFromRows(batch)); err != nil {
			return fmt.Errorf("copy native %s batch: %w", source, err)
		}
		delete(w.batches, source)
	}
	w.rows, w.bytes = 0, 0
	return nil
}

func (w *Writer) Quality(ctx context.Context, source string, metadata model.Metadata) error {
	table, exists := entityTables[source]
	if !exists || table.runtime {
		return fmt.Errorf("unregistered native quality collection %q", source)
	}
	availability, completeness, freshness := "available", "complete", "current"
	if value, ok := metadata["availability"].(string); ok {
		availability = value
	}
	if value, ok := metadata["completeness"].(string); ok {
		completeness = value
	}
	if value, ok := metadata["freshness"].(string); ok {
		freshness = value
	}
	if freshness == "fresh" {
		freshness = "current"
	}
	instants := map[string]any{}
	for _, field := range []string{"as-of", "retrieved-at"} {
		value, _ := metadata[field].(string)
		if field == "as-of" && value == "" {
			value, _ = metadata["observed-at"].(string)
		}
		if value != "" {
			instant, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return errors.New("invalid input quality timestamp")
			}
			instants[field] = instant
		}
	}
	_, err := w.tx.Exec(ctx, `INSERT INTO cao_quality(namespace,collection,availability,completeness,freshness,as_of,retrieved_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(namespace,collection) DO UPDATE SET
		availability=excluded.availability,completeness=excluded.completeness,freshness=excluded.freshness,as_of=excluded.as_of,retrieved_at=excluded.retrieved_at`,
		w.store.namespace, source, availability, completeness, freshness, instants["as-of"], instants["retrieved-at"])
	return err
}

func (w *Writer) Publish(ctx context.Context, dataRevision string) (State, error) {
	if dataRevision == "" {
		return State{}, errors.New("data revision is required")
	}
	if err := w.Flush(ctx); err != nil {
		return State{}, err
	}
	if err := w.restoreAdmittedRuns(ctx); err != nil {
		return State{}, err
	}
	var duplicate bool
	if err := w.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE namespace=$1 GROUP BY id HAVING count(*)>1)`, w.store.namespace).Scan(&duplicate); err != nil {
		return State{}, err
	}
	if duplicate {
		return State{}, errors.New("duplicate run identity across days")
	}
	// Staged run-owned rows are linked after all run records have
	// arrived. The copied batches remain bounded; PostgreSQL routes the insert.
	for _, source := range tableNames() {
		table, ok := entityTables[source]
		if !ok || !w.staged[source] {
			continue
		}
		name := table.name
		columns := []string{"namespace", "ordinal", "present_fields"}
		for _, column := range table.columns {
			columns = append(columns, column.name)
		}
		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = "s." + query.SQLIdentifier(column)
		}
		stage := query.SQLIdentifier(name + "_stage")
		cutoff := dayStart(w.ingestedAt.AddDate(0, 0, -w.store.linkedRetentionDays))
		var expired int64
		if err := w.tx.QueryRow(ctx, "SELECT count(*) FROM "+stage+" s JOIN runs p ON p.namespace=s.namespace AND p.id=s.run_id WHERE p.run_at<$1", cutoff).Scan(&expired); err != nil {
			return State{}, err
		}
		statement := fmt.Sprintf("INSERT INTO %s (%s,run_at) SELECT %s,p.run_at FROM %s s JOIN runs p ON p.namespace=s.namespace AND p.id=s.run_id WHERE p.run_at >= $1",
			query.SQLIdentifier(name), strings.Join(columns, ","), strings.Join(quoted, ","),
			stage)
		tag, err := w.tx.Exec(ctx, statement, cutoff)
		if err != nil {
			return State{}, fmt.Errorf("publish run-owned %s: %w", name, err)
		}
		if tag.RowsAffected()+expired != w.ordinals[source] {
			return State{}, fmt.Errorf("run-owned %s references a missing or ambiguous parent", name)
		}
		if err := w.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+query.SQLIdentifier(name)+" WHERE namespace=$1 GROUP BY id HAVING count(*)>1)", w.store.namespace).Scan(&duplicate); err != nil {
			return State{}, err
		}
		if duplicate {
			return State{}, fmt.Errorf("duplicate %s identity across days", name)
		}
		w.staged[source] = false
	}
	for source := range w.inventoryTables {
		table := entityTables[source]
		columns := []string{"namespace", "ordinal", "present_fields"}
		projection := []string{"i.namespace", fmt.Sprintf("%d + row_number() OVER(ORDER BY i.ordinal) - 1", w.ordinals[source]), "i.present_fields"}
		var updates []string
		var bits []string
		var inventoryBits []string
		for index, column := range table.columns {
			name := query.SQLIdentifier(column.name)
			columns = append(columns, name)
			inputValue, inputPresence := "i."+name, "get_bit(i.present_fields,"+strconv.Itoa(index)+")=1"
			if column.field == "observedAt" {
				inputValue = "CASE WHEN " + inputPresence + " THEN i." + name + " ELSE coalesce(q.as_of,q.retrieved_at) END"
				inputPresence = "(" + inputPresence + " OR coalesce(q.as_of,q.retrieved_at) IS NOT NULL)"
			}
			projection = append(projection, inputValue)
			inventoryBits = append(inventoryBits, "CASE WHEN "+inputPresence+" THEN '1' ELSE '0' END")
			if column.field != "id" {
				identity := source == "$repositories" && (column.field == "owner" || column.field == "name") ||
					source == "$workflows" && (column.field == "repositoryId" || column.field == "path") ||
					source == "$campaigns" && column.field == "slug"
				if identity {
					updates = append(updates, name+"=CASE WHEN get_bit(t.present_fields,"+strconv.Itoa(index)+")=1 THEN t."+name+" ELSE excluded."+name+" END")
				} else {
					updates = append(updates, name+"=CASE WHEN get_bit(excluded.present_fields,"+strconv.Itoa(index)+")=1 AND excluded."+name+" IS NOT NULL THEN excluded."+name+" ELSE t."+name+" END")
				}
			}
			projection[2] = "(" + strings.Join(inventoryBits, " || ") + ")::bit varying"
			bits = append(bits, "CASE WHEN get_bit(t.present_fields,"+strconv.Itoa(index)+")=1 OR get_bit(excluded.present_fields,"+strconv.Itoa(index)+")=1 THEN '1' ELSE '0' END")
		}
		updates = append(updates, "present_fields=("+strings.Join(bits, " || ")+")::bit varying")
		statement, args, err := sqlbuilder.Build("INSERT INTO {} AS t({}) SELECT {} FROM {} i LEFT JOIN cao_quality q ON q.namespace=i.namespace AND q.collection={} WHERE i.namespace={} ON CONFLICT(namespace,id) DO UPDATE SET {}",
			sqlbuilder.Identifier(table.name), sqlbuilder.Fragment(strings.Join(columns, ",")),
			sqlbuilder.Fragment(strings.Join(projection, ",")), sqlbuilder.Identifier(table.name+"_inventory"),
			source, w.store.namespace, sqlbuilder.Fragment(strings.Join(updates, ",")))
		if err != nil {
			return State{}, err
		}
		if _, err := w.tx.Exec(ctx, statement, args...); err != nil {
			return State{}, fmt.Errorf("publish typed inventory %s: %w", source, err)
		}
	}
	if err := w.restoreValuePublications(ctx); err != nil {
		return State{}, err
	}
	// Autovacuum cannot analyze uncommitted replacement rows or partitioned parents.
	// Refresh estimates before reconciliation and integrity joins can choose
	// quadratic cold-start plans.
	if _, err := w.tx.Exec(ctx, "SELECT cao_analyze_native()"); err != nil {
		return State{}, fmt.Errorf("refresh native statistics: %w", err)
	}
	if err := w.reconcileDaily(ctx); err != nil {
		return State{}, err
	}
	if err := w.reconcileCollectionDecisions(ctx); err != nil {
		return State{}, err
	}
	if err := overlayRepositoryLifecycle(ctx, w.store.namespace, func(ctx context.Context, statement string, args ...any) error {
		_, err := w.tx.Exec(ctx, statement, args...)
		return err
	}); err != nil {
		return State{}, err
	}
	// Deferred parent checks and native primary keys reject orphans and duplicates
	// before the publication state becomes visible to readers.
	if _, err := w.tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		return State{}, fmt.Errorf("validate canonical relationships: %w", err)
	}
	for _, check := range []string{
		`SELECT 1 FROM runs r JOIN workflows p ON p.namespace=r.namespace AND p.id=r.workflow_id
			WHERE r.namespace=$1 AND r.repository_id<>p.repository_id LIMIT 1`,
		`SELECT 1 FROM operational_value_evidence e JOIN workflows p ON p.namespace=e.namespace AND p.id=e.workflow_id
			WHERE e.namespace=$1 AND e.repository_id<>p.repository_id LIMIT 1`,
		`SELECT 1 FROM workflow_value_collections e JOIN workflows p ON p.namespace=e.namespace AND p.id=e.workflow_id
			WHERE e.namespace=$1 AND e.repository_id<>p.repository_id LIMIT 1`,
		`SELECT 1 FROM experiment_assignments o JOIN runs r ON r.namespace=o.namespace AND r.id=o.run_id
			JOIN experiments d ON d.namespace=o.namespace AND d.id=o.experiment_id WHERE o.namespace=$1 AND r.workflow_id<>d.workflow_id LIMIT 1`,
		`SELECT 1 FROM grader_observations o JOIN runs r ON r.namespace=o.namespace AND r.id=o.run_id
			JOIN graders d ON d.namespace=o.namespace AND d.id=o.grader_id WHERE o.namespace=$1 AND r.workflow_id<>d.workflow_id LIMIT 1`,
		`SELECT 1 FROM eval_observations o JOIN runs r ON r.namespace=o.namespace AND r.id=o.run_id
			JOIN evals d ON d.namespace=o.namespace AND d.id=o.eval_id WHERE o.namespace=$1 AND r.workflow_id<>d.workflow_id LIMIT 1`,
	} {
		var invalid int
		if err := w.tx.QueryRow(ctx, check, w.store.namespace).Scan(&invalid); err == nil {
			return State{}, errors.New("canonical record references parents from different workflows or repositories")
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return State{}, err
		}
	}
	var clockSources []string
	for _, source := range tableNames() {
		table := entityTables[source]
		var fields []string
		for _, column := range table.columns {
			if column.kind != "timestamp" {
				continue
			}
			switch column.field {
			case "observedAt", "timestamp", "createdAt", "startedAt", "completedAt", "updatedAt", "observed-at", "created-at", "started-at", "ended-at", "provenance.observedAt":
				fields = append(fields, query.SQLIdentifier(column.name))
			}
		}
		if len(fields) > 0 {
			clockSources = append(clockSources, "SELECT max(greatest("+strings.Join(fields, ",")+")) AS instant FROM "+query.SQLIdentifier(table.name)+" WHERE namespace=$1")
		}
	}
	clockSources = append(clockSources, "SELECT max(greatest(as_of,retrieved_at)) AS instant FROM cao_quality WHERE namespace=$1")
	var latest *time.Time
	// Keep partitioned-source plans independent as the daily horizon grows.
	for _, statement := range clockSources {
		var instant *time.Time
		if err := w.tx.QueryRow(ctx, statement, w.store.namespace).Scan(&instant); err != nil {
			return State{}, err
		}
		if instant != nil && (latest == nil || instant.After(*latest)) {
			latest = instant
		}
	}
	w.evaluatedAt = time.Unix(0, 0).UTC()
	if latest != nil {
		w.evaluatedAt = latest.UTC()
	}
	// Keep the source clock even when its latest event is a discarded copy.
	if _, err := w.tx.Exec(ctx, auditCurationStatement(), w.store.namespace); err != nil {
		return State{}, fmt.Errorf("curate native audits before publication: %w", err)
	}
	if err := w.capRuns(ctx); err != nil {
		return State{}, err
	}
	state := State{Ready: true, DataRevision: dataRevision, EvaluatedAt: w.evaluatedAt.UTC(), Counts: map[string]int{}}
	for _, source := range tableNames() {
		if entityTables[source].internal {
			continue
		}
		var count int
		if err := w.tx.QueryRow(ctx, "SELECT count(*) FROM "+query.SQLIdentifier(entityTables[source].name)+" WHERE namespace=$1", w.store.namespace).Scan(&count); err != nil {
			return State{}, err
		}
		state.Counts[source] = count
		if _, err := w.tx.Exec(ctx, `INSERT INTO cao_quality(namespace,collection,availability,completeness,freshness)
			VALUES($1,$2,$3,'complete','current') ON CONFLICT(namespace,collection) DO UPDATE SET
			availability=CASE WHEN cao_quality.availability='unavailable' THEN 'unavailable' ELSE excluded.availability END`,
			w.store.namespace, source, map[bool]string{true: "empty", false: "available"}[count == 0]); err != nil {
			return State{}, err
		}
	}
	if _, err := w.tx.Exec(ctx, `UPDATE cao_quality SET completeness='partial',availability='available'
		WHERE namespace=$1 AND collection IN ('$runs','$workflows','$repositories')
		AND EXISTS(SELECT 1 FROM runs WHERE namespace=$1 AND status='admitted' AND admission_status='admitted')`,
		w.store.namespace); err != nil {
		return State{}, err
	}
	if _, err := w.tx.Exec(ctx, `UPDATE cao_quality SET completeness='partial',freshness='unknown'
		WHERE namespace=$1 AND availability='available'
		AND collection IN ('$runs','$workflows','$repositories','$audits','$graders','$graderObservations')
		AND EXISTS(SELECT 1 FROM audits WHERE namespace=$1 AND source='conclusion-admission' AND source_sequence=0)`,
		w.store.namespace); err != nil {
		return State{}, err
	}
	if err := w.persistStatisticalSnapshot(ctx, dataRevision, query.MaxWorkingBytes); err != nil {
		return State{}, fmt.Errorf("publish statistical bundles: %w", err)
	}
	if err := w.tx.QueryRow(ctx, `UPDATE cao_state SET revision=revision+1,data_revision=$1,evaluated_at=$2,schema_version=$3
		WHERE namespace=$4 RETURNING revision`, dataRevision, w.evaluatedAt, model.SchemaVersion, w.store.namespace).Scan(&state.Revision); err != nil {
		return State{}, err
	}
	if err := w.tx.Commit(ctx); err != nil {
		return State{}, err
	}
	w.tx = nil
	_ = w.connection.Close(ctx)
	w.connection = nil
	return state, nil
}
