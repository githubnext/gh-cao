package sessionarchiveblob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

type queryTable struct {
	TableName string
	Columns   []struct{ ColumnName string }
	Rows      [][]json.RawMessage
}

// query rejects partial results, unrecognized auxiliary tables and server
// diagnostics. Raw Azure bodies (which may contain credentials or evidence)
// are never returned to callers.
func (a *Archive) query(ctx context.Context, path, kql string) (queryTable, error) {
	body, err := json.Marshal(map[string]any{"db": a.config.Database, "csl": kql,
		"properties": map[string]any{"Options": map[string]any{
			"servertimeout":        fmt.Sprintf("00:%02d:%02d", int(a.config.QueryTimeout/time.Minute), int(a.config.QueryTimeout/time.Second)%60),
			"truncationmaxrecords": 4097, "truncationmaxsize": 8 << 20,
			"maxmemoryconsumptionperiterator": 64 << 20, "max_memory_consumption_per_query_per_node": 128 << 20,
			"query_fanout_threads_percent": 10, "query_results_cache_max_age": "00:00:00",
		}}})
	if err != nil || len(body) > 1<<20 {
		return queryTable{}, ErrCapacity
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return queryTable{}, ErrQuery
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-ms-client-request-id", "cao-archive;"+uuid.NewString())
	response, err := a.queryHTTP.Do(request)
	if err != nil {
		return queryTable{}, ErrQuery
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return queryTable{}, ErrQuery
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(content) > 8<<20 {
		return queryTable{}, ErrCapacity
	}
	var envelope struct {
		Tables     []queryTable
		Error      json.RawMessage
		Exceptions []json.RawMessage
	}
	if err := json.Unmarshal(content, &envelope); err != nil || len(envelope.Error) != 0 || len(envelope.Exceptions) != 0 {
		return queryTable{}, ErrQuery
	}
	var result queryTable
	found, status := false, false
	for _, table := range envelope.Tables {
		columns := map[string]int{}
		for i, column := range table.Columns {
			if column.ColumnName == "" {
				return queryTable{}, ErrQuery
			}
			if _, exists := columns[column.ColumnName]; exists {
				return queryTable{}, ErrQuery
			}
			columns[column.ColumnName] = i
		}
		for _, row := range table.Rows {
			if len(row) != len(table.Columns) {
				return queryTable{}, ErrQuery
			}
		}
		if severity, ok := columns["Severity"]; ok {
			if status || len(table.Rows) == 0 {
				return queryTable{}, ErrQuery
			}
			status = true
			for _, row := range table.Rows {
				var level int
				if json.Unmarshal(row[severity], &level) != nil || level <= 2 || level > 6 {
					return queryTable{}, ErrQuery
				}
				if code, ok := columns["StatusCode"]; ok {
					var value int
					if json.Unmarshal(row[code], &value) != nil || value != 0 {
						return queryTable{}, ErrQuery
					}
				}
			}
		} else if _, toc := columns["Ordinal"]; toc {
			// V1 TableOfContents is auxiliary; role attribution below still
			// requires a named result and a complete QueryStatus.
			if _, ok := columns["Kind"]; !ok {
				return queryTable{}, ErrQuery
			}
		} else if table.TableName == "PrimaryResult" || table.TableName == "Table_0" {
			if found {
				return queryTable{}, ErrQuery
			}
			found, result = true, table
		} else {
			return queryTable{}, ErrQuery
		}
	}
	if !found || (path == "/v1/rest/query" && !status) || len(result.Rows) > 4097 {
		return queryTable{}, ErrQuery
	}
	return result, nil
}

func literal(value string) string {
	content, _ := json.Marshal(value)
	return string(content)
}

// EventSchema derives the physical external schema from the generated shared
// Go event contract, never from a parallel maintained KQL field list.
func EventSchema() string {
	t := reflect.TypeFor[sessionarchive.Event]()
	columns := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		var kind string
		switch field.Type.Kind() {
		case reflect.String:
			kind = "string"
		case reflect.Int, reflect.Int64:
			kind = "long"
		case reflect.Slice:
			if field.Type.Elem().Kind() != reflect.String {
				return ""
			}
			kind = "dynamic"
		default:
			return ""
		}
		columns = append(columns, name+":"+kind)
	}
	return strings.Join(columns, ",")
}

// ExternalTableDDL is a setup-only contract. Runtime never executes this DDL.
// The managed identity must have read-only access to this exact private root.
func ExternalTableDDL(c Config) (string, error) {
	if err := c.Validate(); err != nil || !c.Enabled {
		return "", ErrScope
	}
	schema := EventSchema()
	if schema == "" {
		return "", ErrConformance
	}
	return ".create external table " + c.Table + " (" + schema + ")\n" +
		"kind=storage partition by (ArchiveNamespace:string,ArchiveRepository:string,ArchiveDay:string,ArchiveObject:string)\n" +
		`pathformat=("objects/" ArchiveNamespace "/" ArchiveRepository "/" ArchiveDay "/" ArchiveObject "/") dataformat=parquet` + "\n(" +
		literal(c.blobRoot()+";managed_identity=system") + ")\n" +
		`with (fileExtension="parquet", namePrefix="events", docString="` + planVersion + `")`, nil
}

func (a *Archive) verifyTable(ctx context.Context) error {
	if !digestPattern.MatchString(a.config.ExternalPropertiesDigest) {
		return ErrConformance
	}
	table, err := a.query(ctx, "/v1/rest/mgmt", ".show external table "+a.config.Table+" | project TableName, TableType, Properties")
	if err != nil || len(table.Rows) != 1 || len(table.Columns) != 3 {
		return ErrConformance
	}
	var name, kind, properties string
	row := table.Rows[0]
	if json.Unmarshal(row[0], &name) != nil || json.Unmarshal(row[1], &kind) != nil ||
		json.Unmarshal(row[2], &properties) != nil || name != a.config.Table ||
		!slices.Contains([]string{"Blob", "Storage", "blob", "storage"}, kind) ||
		digest([]byte(properties)) != a.config.ExternalPropertiesDigest {
		return ErrConformance
	}
	if !a.exactStorageProperties(properties) {
		return ErrConformance
	}
	table, err = a.query(ctx, "/v1/rest/mgmt", ".show external table "+a.config.Table+" schema as csl")
	if err != nil || len(table.Rows) != 1 || len(table.Rows[0]) < 2 {
		return ErrConformance
	}
	var schema string
	if json.Unmarshal(table.Rows[0][1], &schema) != nil {
		return ErrConformance
	}
	schema = strings.ReplaceAll(strings.ReplaceAll(schema, " ", ""), "\n", "")
	// Virtual columns may be included by the engine's schema presentation.
	expected := EventSchema()
	if expected == "" || (schema != expected && schema != expected+",ArchiveNamespace:string,ArchiveRepository:string,ArchiveDay:string,ArchiveObject:string") {
		return ErrConformance
	}
	return nil
}

func (a *Archive) exactStorageProperties(properties string) bool {
	var document any
	if json.Unmarshal([]byte(properties), &document) != nil {
		return false
	}
	expected := a.config.blobRoot() + ";managed_identity=system"
	paths, valid := 0, true
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for _, item := range value {
				visit(item)
			}
		case []any:
			for _, item := range value {
				visit(item)
			}
		case string:
			if strings.Contains(value, "://") {
				paths++
				if value != expected {
					valid = false
				}
			}
		}
	}
	visit(document)
	// Unknown/redacted URI encodings fail closed. Destination conformance
	// must establish the actual Properties representation before activation.
	return valid && paths == 1
}

func membershipKQL(table string, files []sessionarchive.File, members []sessionarchive.Member) string {
	objects := make([]string, 0, len(files))
	days := make([]string, 0, len(files))
	rows := make([]string, 0, len(members))
	for _, f := range files {
		objects = append(objects, literal(f.Digest))
		day := literal(fileDay(f))
		if !slices.Contains(days, day) {
			days = append(days, day)
		}
	}
	for _, m := range members {
		rows = append(rows, strings.Join([]string{literal(m.Namespace), literal(m.Repository),
			literal(m.RunID), strconv.FormatInt(m.Attempt, 10), literal(m.Version), literal(m.ID)}, ","))
	}
	// The explicit virtual-partition predicate prunes to exact immutable
	// objects. The inner join is unique on complete canonical binding and is
	// applied before any summary/order/limit; no global DISTINCT is needed.
	return "let selected=datatable(Namespace:string,Repository:string,RunId:string,RunAttempt:long,ProjectionId:string,TraceId:string)[" +
		strings.Join(rows, ",") + "];\nexternal_table(" + literal(table) + ")\n" +
		"| where ArchiveNamespace==" + literal(digest([]byte(members[0].Namespace))) +
		" and ArchiveRepository==" + literal(digest([]byte(members[0].Repository))) +
		" and ArchiveDay in (" + strings.Join(days, ",") + ")\n" +
		"| where ArchiveObject in (" + strings.Join(objects, ",") + ")\n" +
		"| join kind=inner (selected) on Namespace,Repository,RunId,RunAttempt,ProjectionId,TraceId\n"
}

func fileVerificationKQL(table string, file sessionarchive.File) string {
	return membershipKQL(table, []sessionarchive.File{file}, file.Members) +
		"| summarize Copies=count(),Invalid=countif(ContractVersion!=1 or hash_sha256(PayloadJSON)!=ContentDigest or CompletionDay!=" + literal(file.Day) + ")\n" +
		"  by Namespace,Repository,RunId,RunAttempt,ProjectionId,TraceId,Sequence\n" +
		"| summarize Events=sum(Copies),Unique=count(),First=min(Sequence),Last=max(Sequence),Invalid=sum(Invalid)\n" +
		"  by Namespace,Repository,RunId,RunAttempt,ProjectionId,TraceId\n" +
		"| project Namespace,Repository,RunId,RunAttempt,ProjectionId,TraceId,Events,Unique,First,Last,Invalid"
}

func (a *Archive) verifyFile(ctx context.Context, file sessionarchive.File) error {
	table, err := a.query(ctx, "/v1/rest/query", fileVerificationKQL(a.config.Table, file))
	if err != nil {
		return err
	}
	if len(table.Columns) != 11 || len(table.Rows) != len(file.Members) {
		return ErrIntegrity
	}
	seen := map[sessionarchive.Member]bool{}
	for _, row := range table.Rows {
		var m sessionarchive.Member
		var unique, first, last, invalid int64
		targets := []any{&m.Namespace, &m.Repository, &m.RunID, &m.Attempt, &m.Version, &m.ID,
			&m.Events, &unique, &first, &last, &invalid}
		for i, target := range targets {
			if json.Unmarshal(row[i], target) != nil {
				return ErrIntegrity
			}
		}
		if !slices.Contains(file.Members, m) || seen[m] || unique != m.Events ||
			first != 0 || last != m.Events-1 || invalid != 0 {
			return ErrIntegrity
		}
		seen[m] = true
	}
	return nil
}

type Reader struct {
	archive *Archive
	id      string
	ctx     context.Context
}

func (r *Reader) Close() error {
	r.archive.mu.Lock()
	defer r.archive.mu.Unlock()
	if active, ok := r.archive.readers[r.id]; ok {
		active.cancel()
		delete(r.archive.readers, r.id)
	}
	return nil
}

// Deliver serializes the final access check and result delivery with withdrawal.
// The callback must not call back into the archive.
func (r *Reader) Deliver(deliver func() error) error {
	a := r.archive
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ready(); err != nil {
		return err
	}
	active, ok := a.readers[r.id]
	if !ok {
		return context.Canceled
	}
	for _, m := range active.members {
		if slices.Contains(a.state.WithdrawnRepositories, m.Repository) {
			return ErrWithdrawn
		}
		for _, f := range a.state.Fences {
			if f.Withdrawn && f.Namespace == m.Namespace && f.Repository == m.Repository && f.RunID == m.RunID && f.Attempt == m.Attempt {
				return ErrWithdrawn
			}
		}
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	return deliver()
}

func (a *Archive) pin(ctx context.Context, scope sessionarchive.Scope, repository, runID string, attempt int64) (context.Context, uint64, []sessionarchive.File, []sessionarchive.Member, *Reader, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ready(); err != nil {
		return nil, 0, nil, nil, nil, err
	}
	if scope.Namespace != a.config.Namespace || !slices.Contains(scope.Repositories, repository) ||
		!a.config.authorized(scope.Namespace, repository) {
		return nil, 0, nil, nil, nil, ErrScope
	}
	if slices.Contains(a.state.WithdrawnRepositories, repository) {
		return nil, 0, nil, nil, nil, ErrWithdrawn
	}
	// Exactly one in-flight query keeps the supported initial profile bounded.
	if len(a.readers) > 0 {
		return nil, 0, nil, nil, nil, ErrCapacity
	}
	var members []sessionarchive.Member
	for _, m := range a.state.Manifest.Members {
		if m.Repository == repository && (runID == "" || m.RunID == runID && m.Attempt == attempt) {
			members = append(members, m)
		}
	}
	var files []sessionarchive.File
	var scanBytes, scanRows int64
	for _, file := range a.state.Manifest.Files {
		if slices.ContainsFunc(file.Members, func(m sessionarchive.Member) bool { return slices.Contains(members, m) }) {
			files = append(files, file)
			scanBytes += file.Bytes
			scanRows += file.Rows
		}
	}
	if scanBytes > a.config.MaxQueryBytes || scanRows > 1_000_000 {
		return nil, 0, nil, nil, nil, ErrCapacity
	}
	if len(members) == 0 {
		return nil, a.active.Generation, nil, nil, nil, sessionarchive.ErrNotFound
	}
	if err := a.blob.private(ctx); err != nil {
		return nil, 0, nil, nil, nil, err
	}
	// Persist the worst-case server execution horizon before issuing KQL.
	// HTTP cancellation is not proof that remote execution has terminated.
	horizon := a.now().Add(a.config.QueryTimeout + 2*time.Minute)
	if err := a.save(ctx, a.state, horizon); err != nil {
		return nil, 0, nil, nil, nil, err
	}
	id := uuid.NewString()
	queryCtx, cancel := context.WithTimeout(ctx, a.config.QueryTimeout)
	a.readers[id] = reader{members: members, cancel: cancel}
	generation := a.active.Generation
	return queryCtx, generation, files, members, &Reader{archive: a, id: id, ctx: queryCtx}, nil
}

func (a *Archive) Timeline(ctx context.Context, scope sessionarchive.Scope, request sessionarchive.TimelineRequest) (uint64, []sessionarchive.Event, error) {
	generation, events, reader, err := a.ReadTimeline(ctx, scope, request)
	if err != nil {
		return generation, nil, err
	}
	defer func() { _ = reader.Close() }()
	if err := reader.Deliver(func() error { return nil }); err != nil {
		return generation, nil, err
	}
	return generation, events, nil
}

// ReadTimeline retains withdrawal protection until the caller closes its reader.
func (a *Archive) ReadTimeline(ctx context.Context, scope sessionarchive.Scope, request sessionarchive.TimelineRequest) (_ uint64, _ []sessionarchive.Event, _ *Reader, err error) {
	if request.RunID == "" || request.Attempt < 1 || request.Limit < 1 || request.Limit > 4096 {
		return 0, nil, nil, sessionarchive.ErrLimit
	}
	queryCtx, generation, files, members, reader, err := a.pin(ctx, scope, request.Repository, request.RunID, request.Attempt)
	if err != nil {
		return generation, nil, nil, err
	}
	defer func() {
		if err != nil {
			_ = reader.Close()
		}
	}()
	if len(members) != 1 || members[0].Events > int64(request.Limit) {
		return generation, nil, nil, sessionarchive.ErrLimit
	}
	if err := a.verifyTable(queryCtx); err != nil {
		return generation, nil, nil, err
	}
	kql := membershipKQL(a.config.Table, files, members) +
		"| order by Sequence asc\n| take " + strconv.Itoa(request.Limit+1) +
		"\n| project " + strings.Join(eventNames(), ",") + "\n| project Event=pack_all()"
	table, err := a.query(queryCtx, "/v1/rest/query", kql)
	if err != nil {
		return generation, nil, nil, err
	}
	if len(table.Columns) != 1 || table.Columns[0].ColumnName != "Event" || len(table.Rows) > request.Limit {
		return generation, nil, nil, sessionarchive.ErrLimit
	}
	var events []sessionarchive.Event
	if int64(len(table.Rows)) != members[0].Events {
		return generation, nil, nil, ErrIntegrity
	}
	for i, row := range table.Rows {
		var event sessionarchive.Event
		raw := row[0]
		if len(raw) > 0 && raw[0] == '"' {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return generation, nil, nil, ErrIntegrity
			}
			raw = json.RawMessage(text)
		}
		if json.Unmarshal(raw, &event) != nil || event.Namespace != scope.Namespace ||
			event.Repository != request.Repository || event.RunID != request.RunID || event.RunAttempt != request.Attempt ||
			event.ProjectionID != members[0].Version || event.TraceID != members[0].ID ||
			event.Sequence != int64(i) || event.ContractVersion != sessionarchive.ContractVersion ||
			!json.Valid([]byte(event.PayloadJSON)) || digest([]byte(event.PayloadJSON)) != event.ContentDigest {
			return generation, nil, nil, ErrIntegrity
		}
		events = append(events, event)
	}
	if err := reader.Deliver(func() error { return nil }); err != nil {
		return generation, nil, nil, err
	}
	return generation, events, reader, nil
}

func (a *Archive) Summaries(ctx context.Context, scope sessionarchive.Scope, repository string, limit int) (uint64, []sessionarchive.Summary, error) {
	generation, summaries, reader, err := a.ReadSummaries(ctx, scope, repository, limit)
	if err != nil {
		return generation, nil, err
	}
	defer func() { _ = reader.Close() }()
	if err := reader.Deliver(func() error { return nil }); err != nil {
		return generation, nil, err
	}
	return generation, summaries, nil
}

// ReadSummaries retains withdrawal protection until the caller closes its reader.
func (a *Archive) ReadSummaries(ctx context.Context, scope sessionarchive.Scope, repository string, limit int) (_ uint64, _ []sessionarchive.Summary, _ *Reader, err error) {
	if limit < 1 || limit > 4096 {
		return 0, nil, nil, sessionarchive.ErrLimit
	}
	queryCtx, generation, files, members, reader, err := a.pin(ctx, scope, repository, "", 0)
	if err != nil {
		return generation, nil, nil, err
	}
	defer func() {
		if err != nil {
			_ = reader.Close()
		}
	}()
	if err := a.verifyTable(queryCtx); err != nil {
		return generation, nil, nil, err
	}
	table, err := a.query(queryCtx, "/v1/rest/query", membershipKQL(a.config.Table, files, members)+
		"| summarize Events=count() by Repository,RunId,RunAttempt,ProjectionId\n"+
		"| order by RunId asc,RunAttempt asc\n| take "+strconv.Itoa(limit+1))
	if err != nil {
		return generation, nil, nil, err
	}
	if len(table.Columns) != 5 || len(table.Rows) > limit {
		return generation, nil, nil, sessionarchive.ErrLimit
	}
	var summaries []sessionarchive.Summary
	if len(table.Rows) != len(members) {
		return generation, nil, nil, ErrIntegrity
	}
	seen := map[string]bool{}
	for _, row := range table.Rows {
		var summary sessionarchive.Summary
		if json.Unmarshal(row[0], &summary.Repository) != nil || json.Unmarshal(row[1], &summary.RunID) != nil ||
			json.Unmarshal(row[2], &summary.Attempt) != nil || json.Unmarshal(row[3], &summary.ProjectionID) != nil ||
			json.Unmarshal(row[4], &summary.Events) != nil {
			return generation, nil, nil, ErrIntegrity
		}
		var selected *sessionarchive.Member
		for _, member := range members {
			if member.Repository == summary.Repository && member.RunID == summary.RunID &&
				member.Attempt == summary.Attempt && member.Version == summary.ProjectionID {
				selected = &member
				break
			}
		}
		if selected == nil || seen[memberKey(*selected)] || selected.Events != summary.Events {
			return generation, nil, nil, ErrIntegrity
		}
		seen[memberKey(*selected)] = true
		summaries = append(summaries, summary)
	}
	if err := reader.Deliver(func() error { return nil }); err != nil {
		return generation, nil, nil, err
	}
	return generation, summaries, reader, nil
}

func eventNames() []string {
	t := reflect.TypeFor[sessionarchive.Event]()
	var names []string
	for i := 0; i < t.NumField(); i++ {
		names = append(names, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	return names
}
