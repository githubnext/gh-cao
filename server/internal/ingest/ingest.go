package ingest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/repositorymemory"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var ingestLog = logger.New("cao:ingest")

type Result struct {
	Revision     int64          `json:"revision"`
	DataRevision string         `json:"dataRevision"`
	EvaluatedAt  string         `json:"evaluatedAt"`
	Counts       map[string]int `json:"counts"`
}
type Options struct {
	DatabaseQueriesPath string
	Force               bool
	// Server-owned evidence is independent of the Activity snapshot contract.
	SupplementRevision string
	Supplement         func(context.Context, *postgresx.Writer) error
}
type Manifest map[string]string

func ValidateManifest(directory string) (Manifest, []string, []string, error) {
	// #nosec G304 -- the operator selects the deployment directory.
	content, err := os.ReadFile(filepath.Join(directory, "payload-hashes.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read payload manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, nil, nil, fmt.Errorf("parse payload manifest: %w", err)
	}
	if len(manifest) == 0 {
		return nil, nil, nil, errors.New("payload manifest is empty")
	}
	var runs, records []string
	for name, expected := range manifest {
		clean := filepath.ToSlash(filepath.Clean(name))
		if clean != name || filepath.IsAbs(name) || strings.HasPrefix(clean, "../") {
			return nil, nil, nil, fmt.Errorf("manifest path %q is unsafe", name)
		}
		if len(expected) != 64 {
			return nil, nil, nil, fmt.Errorf("manifest hash for %q is not SHA-256", name)
		}
		if _, err := hex.DecodeString(expected); err != nil {
			return nil, nil, nil, fmt.Errorf("manifest hash for %q is not SHA-256", name)
		}
		shard := false
		switch {
		case strings.HasPrefix(name, "gh-aw-logs-runs/") && strings.HasSuffix(name, ".jsonl"):
			runs = append(runs, name)
			shard = true
		case strings.HasPrefix(name, "gh-aw-logs-records/") && strings.HasSuffix(name, ".jsonl"):
			records = append(records, name)
			shard = true
		case strings.HasPrefix(name, "gh-aw-logs-shards/"):
			return nil, nil, nil, errors.New("raw activity JSONL is not supported; compacted run/record shards are required")
		}
		if shard {
			actual, err := fileHash(filepath.Join(directory, filepath.FromSlash(name)))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("read manifested payload %q: %w", name, err)
			}
			if !strings.EqualFold(actual, expected) {
				return nil, nil, nil, fmt.Errorf("payload hash mismatch for %q", name)
			}
		}
	}
	if len(runs) == 0 {
		return nil, nil, nil, errors.New("activity shard manifest is missing compacted run-information shards")
	}
	sort.Strings(runs)
	sort.Strings(records)
	return manifest, runs, records, nil
}

func fileHash(path string) (string, error) {
	// #nosec G304 -- paths are operator-selected or clean manifest-relative paths.
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func DirectoryRevision(manifest Manifest, inventory []byte, additionalRevisions ...string) string {
	sum := sha256.Sum256(inventory)
	return directoryRevision(manifest, hex.EncodeToString(sum[:]), additionalRevisions...)
}
func directoryRevision(manifest Manifest, inventoryHash string, additionalRevisions ...string) string {
	keys := make([]string, 0, len(manifest))
	for key := range manifest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, "canonical-session-types:1\x00")
	for _, key := range keys {
		_, _ = io.WriteString(hasher, key+"\x00"+strings.ToLower(manifest[key])+"\x00")
	}
	_, _ = io.WriteString(hasher, "inventory\x00"+inventoryHash)
	for _, revision := range additionalRevisions {
		_, _ = io.WriteString(hasher, "\x00additional\x00"+revision)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func Run(ctx context.Context, store *postgresx.Store, directory string, options Options) (result Result, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, telemetry.SpanIngestRun)
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "ingestion failed")
		} else {
			span.SetAttributes(attribute.Int64("cao_dashboard.ingest.revision", result.Revision), attribute.Int("cao_dashboard.ingest.source_count", len(result.Counts)))
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}()
	manifest, runs, records, err := ValidateManifest(directory)
	if err != nil {
		return Result{}, err
	}
	inventoryPath := filepath.Join(directory, "inventory-sources.json")
	inventoryHash, err := fileHash(inventoryPath)
	if err != nil {
		return Result{}, fmt.Errorf("read inventory-sources.json: %w", err)
	}
	memory, err := repositorymemory.Load(directory)
	if err != nil {
		return Result{}, err
	}
	dataRevision := directoryRevision(manifest, inventoryHash, memory.Revision)
	if options.SupplementRevision != "" {
		if options.Supplement == nil {
			return Result{}, errors.New("supplement revision requires an evidence adapter")
		}
		dataRevision = directoryRevision(manifest, inventoryHash, memory.Revision, options.SupplementRevision)
	} else if options.Supplement != nil {
		return Result{}, errors.New("supplement requires a content revision")
	}
	active, err := store.State(ctx)
	if err != nil {
		return Result{}, err
	}
	if !options.Force && active.Ready && active.DataRevision == dataRevision {
		active, err = store.CurateAudits(ctx)
		if err != nil {
			return Result{}, err
		}
		return stateResult(active), nil
	}
	writer, err := store.BeginIngestion(ctx)
	if err != nil {
		return Result{}, err
	}
	defer writer.Abort(ctx)
	if !options.Force && writer.PreviousDataRevision == dataRevision {
		writer.Abort(ctx)
		active, err := store.CurateAudits(ctx)
		if err != nil {
			return Result{}, err
		}
		return stateResult(active), nil
	}
	if len(records) == 0 {
		for _, source := range []string{"$domains", "$tools", "$skills", "$friction", "$audits", "$issues", "$graders", "$graderObservations", "$evals", "$evalObservations", "$operationalValues"} {
			if err := writer.Quality(ctx, source, model.Metadata{"availability": "unavailable", "completeness": "unknown", "freshness": "unknown"}); err != nil {
				return Result{}, err
			}
		}
	}
	// Canonical run identity is authoritative and always precedes linked records.
	for _, phase := range [][]string{runs, records} {
		for _, name := range phase {
			if err := readShard(ctx, writer, filepath.Join(directory, filepath.FromSlash(name)), manifest[name]); err != nil {
				return Result{}, err
			}
		}
		if err := writer.Flush(ctx); err != nil {
			return Result{}, err
		}
	}
	_, err = loadDefinitions(options.DatabaseQueriesPath)
	if err != nil {
		return Result{}, err
	}
	if err := readInventory(ctx, writer, inventoryPath, inventoryHash); err != nil {
		return Result{}, err
	}
	if options.Supplement != nil {
		if err := options.Supplement(ctx, writer); err != nil {
			return Result{}, fmt.Errorf("project server evidence: %w", err)
		}
	}
	active, err = writer.Publish(ctx, dataRevision)
	if err != nil {
		return Result{}, err
	}
	ingestLog.Printf("published native revision=%d collections=%d", active.Revision, len(active.Counts))
	return stateResult(active), nil
}

func stateResult(state postgresx.State) Result {
	return Result{Revision: state.Revision, DataRevision: state.DataRevision, EvaluatedAt: state.EvaluatedAt.UTC().Format(time.RFC3339Nano), Counts: state.Counts}
}

func loadDefinitions(path string) ([]query.Definition, error) {
	if path == "" {
		return nil, errors.New("database query path is required for typed inventory adapters")
	}
	// #nosec G304 -- the operator configures this local definition file.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read database queries: %w", err)
	}
	var definitions []query.Definition
	if err := json.Unmarshal(content, &definitions); err != nil {
		return nil, fmt.Errorf("parse database queries: %w", err)
	}
	return definitions, nil
}

func readShard(ctx context.Context, writer *postgresx.Writer, path, expected string) error {
	// #nosec G304 -- manifest-relative paths were validated by ValidateManifest.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(file, hasher))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	var expectedRecords *int64
	var count int64
	header := false
	kind := "records"
	if clean := filepath.ToSlash(path); strings.HasPrefix(clean, "gh-aw-logs-runs/") || strings.Contains(clean, "/gh-aw-logs-runs/") {
		kind = "runs"
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var envelope struct {
			Kind       string          `json:"kind"`
			Collection string          `json:"collection"`
			Record     json.RawMessage `json:"record"`
			Records    *int64          `json:"records"`
			Phase      string          `json:"phase"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return fmt.Errorf("%s:%d must contain valid canonical JSON", path, line)
		}
		switch envelope.Kind {
		case "metadata":
			if header || count != 0 || envelope.Records == nil || *envelope.Records < 0 ||
				envelope.Phase != "" && envelope.Phase != kind {
				return errors.New("normalized shard metadata is invalid")
			}
			header = true
			expectedRecords = envelope.Records
			continue
		case "record":
			if !header {
				return errors.New("normalized shard requires its metadata header before records")
			}
		default:
			return fmt.Errorf("%s:%d has unsupported canonical envelope", path, line)
		}
		if !postgresx.IsCanonicalCollection(envelope.Collection) {
			return errors.New("normalized shard collection is not registered by TypeSpec")
		}
		runCollection := false
		switch envelope.Collection {
		case "campaigns", "repositories", "workflows", "runs", "experiments", "experimentAssignments":
			runCollection = true
		}
		if runCollection != (kind == "runs") {
			return errors.New("normalized collection is in the wrong shard phase")
		}
		decoder := json.NewDecoder(bytes.NewReader(envelope.Record))
		decoder.UseNumber()
		var row model.Row
		if err := decoder.Decode(&row); err != nil || row == nil {
			return fmt.Errorf("%s:%d record must be an object", path, line)
		}
		if err := writer.Append(ctx, "$"+envelope.Collection, row); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected) {
		return fmt.Errorf("payload hash mismatch while ingesting %q", path)
	}
	if expectedRecords == nil || *expectedRecords != count {
		return errors.New("normalized shard record count does not match its metadata")
	}
	scope := filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(path)), filepath.Base(path)))
	identity := sha256.Sum256([]byte(scope + "\x00" + strings.ToLower(expected)))
	return writer.Append(ctx, "$transactions", model.Row{"id": "shard:" + hex.EncodeToString(identity[:]), "kind": kind,
		"payloadScope": scope,
		"payloadHash":  strings.ToLower(expected), "records": count, "committedRecords": count})
}

func skipValue(decoder *json.Decoder, budget *boundedJSONReader) error {
	budget.remaining = maxJSONRecordBytes
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); ok && (delimiter == '{' || delimiter == '[') {
		for decoder.More() {
			if err := skipValue(decoder, budget); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	}
	return err
}

const maxJSONRecordBytes = 16 << 20

type boundedJSONReader struct {
	reader    io.Reader
	remaining int
}

func (reader *boundedJSONReader) Read(target []byte) (int, error) {
	if reader.remaining <= 0 {
		return 0, errors.New("inventory JSON value exceeds the bounded record budget")
	}
	count, err := reader.reader.Read(target[:min(len(target), reader.remaining)])
	reader.remaining -= count
	return count, err
}

func readInventory(ctx context.Context, writer *postgresx.Writer, path, expected string) error {
	// #nosec G304 -- the inventory path is fixed within the deployment directory.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	budget := &boundedJSONReader{reader: io.TeeReader(file, hasher), remaining: maxJSONRecordBytes}
	decoder := json.NewDecoder(budget)
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("inventory-sources.json must be an object")
	}
	for decoder.More() {
		budget.remaining = maxJSONRecordBytes
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("inventory source name must be text")
		}
		switch name {
		case "campaigns", "repositories", "workflows":
		default:
			// Unconsumed report projections and run/experiment substitutes are
			// not admitted as canonical database authority.
			if err := skipValue(decoder, budget); err != nil {
				return err
			}
			continue
		}
		first, err := decoder.Token()
		if err != nil {
			return err
		}
		qualitySource := "$" + name
		if err := writer.Quality(ctx, qualitySource, model.Metadata{}); err != nil {
			return err
		}
		rows := func() error {
			for decoder.More() {
				budget.remaining = maxJSONRecordBytes
				if err := ctx.Err(); err != nil {
					return err
				}
				var row model.Row
				if err := decoder.Decode(&row); err != nil || row == nil {
					return errors.New("inventory row must be an object")
				}
				source, normalized, err := inventoryRow(name, row)
				if err != nil {
					return err
				}
				if err := writer.AppendInventory(ctx, source, normalized); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("inventory rows must be an array")
			}
			return nil
		}
		switch first {
		case json.Delim('['):
			if err := rows(); err != nil {
				return err
			}
		case json.Delim('{'):
			for decoder.More() {
				budget.remaining = maxJSONRecordBytes
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				if key == "rows" {
					start, err := decoder.Token()
					if err != nil || start != json.Delim('[') {
						return errors.New("inventory rows must be an array")
					}
					if err := rows(); err != nil {
						return err
					}
				} else if key == "metadata" {
					var metadata model.Metadata
					if err := decoder.Decode(&metadata); err != nil {
						return err
					}
					source := "$" + name
					if err := writer.Quality(ctx, source, metadata); err != nil {
						return err
					}
				} else if err := skipValue(decoder, budget); err != nil {
					return err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return err
			}
		default:
			return errors.New("inventory input must be a row array or a logical-source envelope")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("inventory contains trailing JSON")
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected) {
		return errors.New("inventory changed while ingesting")
	}
	return nil
}

// inventoryRow is mechanical field normalization, not a query evaluator.
// Canonical IDs use the same stable coordinate rules as ingestion.json.
func inventoryRow(name string, input model.Row) (string, model.Row, error) {
	source := "$" + name
	var row model.Row
	switch name {
	case "campaigns", "repositories", "workflows":
		var err error
		row, err = postgresx.NormalizeInventoryFields(name, input)
		if err != nil {
			return "", nil, err
		}
		if observed, present := input["observed-at"]; present {
			row["observedAt"] = observed
		}
		text := func(field string) string { value, _ := input[field].(string); return strings.TrimSpace(value) }
		switch name {
		case "campaigns":
			slug := text("campaign")
			if slug == "" {
				return "", nil, errors.New("inventory campaign requires a slug")
			}
			row["id"], row["slug"] = "campaign:dashboard-sources:"+model.EncodeCoordinate(slug), slug
			if _, present := row["name"]; !present {
				row["name"] = slug
			}
		case "repositories":
			owner, repository := text("organization"), text("repository")
			if owner == "" || repository == "" {
				return "", nil, errors.New("inventory repository requires a coordinate")
			}
			row["id"], row["owner"], row["name"] = "repository:"+model.EncodeCoordinate(strings.ToLower(owner+"/"+repository)), owner, repository
		case "workflows":
			owner, repository, path := text("organization"), text("repository"), strings.ToLower(text("workflow"))
			path = strings.TrimSuffix(path, ".lock.yml") + ".md"
			if !strings.HasSuffix(text("workflow"), ".lock.yml") {
				path = strings.ToLower(text("workflow"))
			}
			if owner == "" || repository == "" || path == "" {
				return "", nil, errors.New("inventory workflow requires a coordinate")
			}
			coordinate := strings.ToLower(owner + "/" + repository)
			row["id"], row["repositoryId"], row["path"] = "workflow:"+model.EncodeCoordinate(coordinate+":"+path), "repository:"+model.EncodeCoordinate(coordinate), path
			campaign := text("campaign")
			if campaign == "" {
				campaign = text("package")
			}
			if campaign != "" {
				row["campaignId"] = "campaign:dashboard-sources:" + model.EncodeCoordinate(campaign)
			}
			if _, present := row["name"]; !present {
				row["name"] = text("workflow")
			}
			switch text("workflow-active") {
			case "true":
				row["state"] = "active"
			case "false":
				row["state"] = "disabled"
			default:
				row["state"] = "unknown"
			}
		}
	default:
		return "", nil, fmt.Errorf("no canonical inventory adapter for %q", name)
	}
	return source, row, nil
}
