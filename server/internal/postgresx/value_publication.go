package postgresx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

var (
	ErrValueInvalid       = errors.New("invalid operational value evidence")
	ErrValueConflict      = errors.New("operational value evidence conflicts with retained evidence")
	ErrValueParentMissing = errors.New("operational value repository or workflow has not been observed")
	ErrValueNotFound      = errors.New("no canonical operational value evidence is available")
)

type ValuePublication struct {
	ID         string `json:"id"`
	Revision   int64  `json:"revision"`
	Updated    bool   `json:"updated"`
	ObservedAt string `json:"observedAt"`
}

type valueProofPublication struct {
	repository string
	workflow   string
	files      []ValueProofFile
	updatedAt  time.Time
}

func valueFactKey(fact map[string]any) string {
	return fmt.Sprintf("%q:%q:%q", fact["fact-kind"], evidenceText(fact["parent-id"]), fact["fact-id"])
}

func decodeValueJSON(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func canonicalValueFact(fact map[string]any) map[string]any {
	result := maps.Clone(fact)
	for _, column := range entityTables["$operationalValueEvidence.facts"].columns {
		if column.kind == "numeric" && fact[column.field] != nil {
			result[column.field] = canonicalValueNumber(fact[column.field])
		}
	}
	return result
}

// Canonical decimal coefficient/exponent avoids float64 rounding and never
// expands an attacker-supplied exponent into an enormous intermediate integer.
func canonicalValueNumber(value any) string {
	mantissa, exponent, _ := strings.Cut(strings.ToLower(fmt.Sprint(value)), "e")
	power, _ := strconv.ParseInt(exponent, 10, 64)
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0"
	}
	short := strings.TrimRight(digits, "0")
	power += int64(len(digits) - len(short) - len(fraction))
	return sign + short + "e" + strconv.FormatInt(power, 10)
}

// Normalize only representation, never evidence clocks or authority.
func normalizeValuePublication(input model.Row) (model.Row, string, error) {
	if err := validateOperationalValueEvidence(input); err != nil {
		return nil, "", ErrValueInvalid
	}
	if len(evidenceText(input["id"])) > 2048 {
		return nil, "", ErrValueInvalid
	}
	capture, _ := evidenceTime(input["observedAt"])
	if capture.After(time.Now()) {
		return nil, "", ErrValueInvalid
	}
	content, _ := json.Marshal(input)
	var row model.Row
	if err := decodeValueJSON(content, &row); err != nil {
		return nil, "", ErrValueInvalid
	}
	// Ingestion provenance is not part of this typed packet's storage contract.
	delete(row, "provenance")
	repository := strings.ToLower(evidenceText(row["repository"]))
	row["repository"] = repository
	row["repositoryId"] = "repository:" + evidenceComponent(repository)
	row["workflowId"] = "workflow:" + evidenceComponent(repository+":"+evidenceText(row["sourcePath"]))
	for _, field := range []string{"observedAt", "windowStart", "windowEnd"} {
		at, _ := evidenceTime(row[field])
		row[field] = at.Format(time.RFC3339Nano)
	}
	facts := row["facts"].([]any)
	for _, item := range facts {
		fact := item.(map[string]any)
		for _, column := range entityTables["$operationalValueEvidence.facts"].columns {
			if column.kind == "timestamp" && fact[column.field] != nil {
				at, _ := evidenceTime(fact[column.field])
				fact[column.field] = at.Format(time.RFC3339Nano)
			}
		}
	}
	sort.Slice(facts, func(i, j int) bool {
		return valueFactKey(facts[i].(map[string]any)) < valueFactKey(facts[j].(map[string]any))
	})
	canonical := maps.Clone(row)
	canonicalFacts := make([]any, len(facts))
	for index, item := range facts {
		canonicalFacts[index] = canonicalValueFact(item.(map[string]any))
	}
	canonical["facts"] = canonicalFacts
	content, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", ErrValueInvalid
	}
	return row, fmt.Sprintf("%x", sha256.Sum256(content)), nil
}

func sameValueFact(left, right map[string]any) bool {
	a, _ := json.Marshal(canonicalValueFact(left))
	b, _ := json.Marshal(canonicalValueFact(right))
	return string(a) == string(b)
}

func strengthenedValueOrigin(old, next any) bool {
	return old == next || old == "corroborated" && next == "receipt-verified"
}

func validateValueReplacement(previous, next model.Row) error {
	before, _ := evidenceTime(previous["observedAt"])
	after, _ := evidenceTime(next["observedAt"])
	if !after.After(before) || previous["contractDigest"] != next["contractDigest"] {
		return ErrValueConflict
	}
	index := map[string]map[string]any{}
	for _, item := range next["facts"].([]any) {
		fact := item.(map[string]any)
		index[valueFactKey(fact)] = fact
	}
	start, _ := evidenceTime(next["windowStart"])
	end, _ := evidenceTime(next["windowEnd"])
	oldStart, _ := evidenceTime(previous["windowStart"])
	oldEnd, _ := evidenceTime(previous["windowEnd"])
	if start.After(oldStart) || end.Before(oldEnd) {
		return ErrValueConflict
	}
	for _, item := range previous["facts"].([]any) {
		old := item.(map[string]any)
		kind := evidenceText(old["fact-kind"])
		current := index[valueFactKey(old)]
		immutable := kind == "contract" || kind == "implementation" || kind == "snapshot" ||
			kind == "snapshot-item" || strings.HasPrefix(kind, "implementation-")
		if !immutable && kind != "output" {
			continue
		}
		if current == nil {
			return ErrValueConflict
		}
		left, right := maps.Clone(old), maps.Clone(current)
		if kind == "contract" {
			for _, field := range []string{"outputs-available", "repository-state-available", "checks-available"} {
				if old[field] == true && current[field] != true {
					return ErrValueConflict
				}
				delete(left, field)
				delete(right, field)
			}
			delete(left, "output-coverage")
			delete(right, "output-coverage")
		}
		if kind == "output" || kind == "implementation" {
			if !strengthenedValueOrigin(old["origin"], current["origin"]) {
				return ErrValueConflict
			}
			delete(left, "origin")
			delete(right, "origin")
		}
		if kind == "output" {
			delete(left, "state")
			delete(right, "state")
		}
		if !sameValueFact(left, right) {
			return ErrValueConflict
		}
	}
	return nil
}

func valueJSONProjection(source string) string {
	var fields []string
	for _, column := range entityTables[source].columns {
		if column.field == "packetId" || source == "$operationalValueEvidence.facts" && column.field == "id" {
			continue
		}
		value := "t." + query.SQLIdentifier(column.name)
		if column.kind == "timestamp" {
			value = "to_char(" + value + " AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"')"
		}
		fields = append(fields, "'"+column.field+"'", value)
	}
	return "jsonb_strip_nulls(jsonb_build_object(" + strings.Join(fields, ",") + "))"
}

func readValuePacket(ctx context.Context, tx pgx.Tx, namespace, id string) (model.Row, error) {
	var content []byte
	err := tx.QueryRow(ctx, "SELECT "+valueJSONProjection("$operationalValueEvidence")+
		" FROM operational_value_evidence t WHERE namespace=$1 AND id=$2", namespace, id).Scan(&content)
	if err != nil {
		return nil, err
	}
	var row model.Row
	if err := decodeValueJSON(content, &row); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, "SELECT "+valueJSONProjection("$operationalValueEvidence.facts")+
		" FROM operational_value_evidence_facts t WHERE namespace=$1 AND packet_id=$2 ORDER BY ordinal LIMIT 20001", namespace, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := []any{}
	for rows.Next() {
		var fact map[string]any
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		if err := decodeValueJSON(content, &fact); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	row["facts"] = facts
	normalized, _, err := normalizeValuePublication(row)
	if err != nil {
		return nil, fmt.Errorf("retained operational value evidence is invalid: %w", err)
	}
	return normalized, nil
}

func valueParentKnown(ctx context.Context, tx pgx.Tx, namespace string, row model.Row) (bool, error) {
	var known bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM repositories r JOIN workflows w
		ON w.namespace=r.namespace AND w.repository_id=r.id
		WHERE r.namespace=$1 AND r.id=$2 AND w.id=$3 AND w.path=$4 AND lower(r.owner || '/' || r.name)=$5)`,
		namespace, row["repositoryId"], row["workflowId"], row["sourcePath"], row["repository"]).Scan(&known)
	return known, err
}

// ReadOperationalValue reads one bounded canonical packet for an
// exact observed pair. It neither discovers pairs nor infers collection policy.
func (s *Store) ReadOperationalValue(ctx context.Context, repository, workflow string) (model.Row, error) {
	if !targetCoordinate.MatchString(repository) || !evidenceSlug.MatchString(workflow) {
		return nil, ErrValueInvalid
	}
	repository = strings.ToLower(repository)
	path := ".github/workflows/" + workflow + ".md"
	identity := model.Row{
		"repository": repository, "sourcePath": path,
		"repositoryId": "repository:" + evidenceComponent(repository),
		"workflowId":   "workflow:" + evidenceComponent(repository+":"+path),
	}
	connection, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return nil, err
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	known, err := valueParentKnown(ctx, tx, s.namespace, identity)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, ErrValueParentMissing
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT e.id FROM operational_value_evidence e
		LEFT JOIN cao_value_publications p ON p.namespace=e.namespace AND p.packet_id=e.id
		WHERE e.namespace=$1 AND e.repository_id=$2 AND e.workflow_id=$3
		ORDER BY e.observed_at DESC,p.published_at DESC NULLS LAST,e.id LIMIT 1`,
		s.namespace, identity["repositoryId"], identity["workflowId"]).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrValueNotFound
	} else if err != nil {
		return nil, err
	}
	row, err := readValuePacket(ctx, tx, s.namespace, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return row, nil
}

// PublishOperationalValue publishes measured facts, not an evaluator or report.
// The same namespace lock as snapshot ingestion makes ownership and facts atomic.
func (s *Store) PublishOperationalValue(ctx context.Context, input model.Row) (ValuePublication, error) {
	return s.publishOperationalValue(ctx, input, nil)
}

// PublishOperationalValueWithProof atomically publishes accepted evidence and
// replaces its private reusable acquisition proof.
func (s *Store) PublishOperationalValueWithProof(
	ctx context.Context, input model.Row, repository, workflow string,
	files []ValueProofFile, updatedAt time.Time,
) (ValuePublication, error) {
	if len(repository) > 256 || len(workflow) > 256 ||
		!targetCoordinate.MatchString(repository) || !evidenceSlug.MatchString(workflow) || updatedAt.IsZero() {
		return ValuePublication{}, ErrValueInvalid
	}
	files, err := validateValueProofFiles(files)
	if err != nil {
		return ValuePublication{}, err
	}
	return s.publishOperationalValue(ctx, input, &valueProofPublication{
		repository: strings.ToLower(repository), workflow: workflow, files: files, updatedAt: updatedAt,
	})
}

func (s *Store) publishOperationalValue(
	ctx context.Context, input model.Row, proof *valueProofPublication,
) (ValuePublication, error) {
	row, digest, err := normalizeValuePublication(input)
	if err != nil {
		return ValuePublication{}, err
	}
	if proof != nil && (row["repository"] != proof.repository ||
		row["sourcePath"] != ".github/workflows/"+proof.workflow+".md") {
		return ValuePublication{}, ErrValueInvalid
	}
	connection, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return ValuePublication{}, err
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	tx, err := connection.Begin(ctx)
	if err != nil {
		return ValuePublication{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, lock := range []string{
		`SELECT pg_advisory_xact_lock_shared(712083241, 17484)`,
		`SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`,
	} {
		args := []any{}
		if strings.Contains(lock, "$1") {
			args = append(args, s.namespace)
		}
		if _, err := tx.Exec(ctx, lock, args...); err != nil {
			return ValuePublication{}, err
		}
	}
	result := ValuePublication{ID: evidenceText(row["id"]), ObservedAt: evidenceText(row["observedAt"])}
	known, err := valueParentKnown(ctx, tx, s.namespace, row)
	if err != nil {
		return ValuePublication{}, err
	}
	if !known {
		return ValuePublication{}, ErrValueParentMissing
	}
	if err := tx.QueryRow(ctx, `SELECT revision FROM cao_state WHERE namespace=$1 FOR UPDATE`, s.namespace).Scan(&result.Revision); err != nil {
		return ValuePublication{}, err
	}
	var previousDigest string
	err = tx.QueryRow(ctx, `SELECT digest FROM cao_value_publications WHERE namespace=$1 AND packet_id=$2`, s.namespace, result.ID).Scan(&previousDigest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ValuePublication{}, err
	}
	if err == nil && previousDigest == digest {
		if proof == nil {
			return result, nil
		}
		if err := s.replaceValueProof(ctx, tx, proof.repository, proof.workflow, proof.files, proof.updatedAt); err != nil {
			return ValuePublication{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ValuePublication{}, err
		}
		return result, nil
	}
	previous, err := readValuePacket(ctx, tx, s.namespace, result.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ValuePublication{}, err
	}
	if previous != nil {
		_, existingDigest, err := normalizeValuePublication(previous)
		if err != nil {
			return ValuePublication{}, err
		}
		if digest != existingDigest {
			if err := validateValueReplacement(previous, row); err != nil {
				return ValuePublication{}, err
			}
		}
	}
	for _, statement := range []string{
		`DELETE FROM operational_value_evidence_facts WHERE namespace=$1 AND packet_id=$2`,
		`DELETE FROM operational_value_evidence WHERE namespace=$1 AND id=$2`,
	} {
		if _, err := tx.Exec(ctx, statement, s.namespace, result.ID); err != nil {
			return ValuePublication{}, err
		}
	}
	writer := &Writer{store: s, tx: tx, ordinals: map[string]int64{}, batches: map[string][][]any{}}
	for _, source := range []string{"$operationalValueEvidence", "$operationalValueEvidence.facts"} {
		var ordinal int64
		if err := tx.QueryRow(ctx, "SELECT coalesce(max(ordinal)+1,0) FROM "+query.SQLIdentifier(entityTables[source].name)+" WHERE namespace=$1",
			s.namespace).Scan(&ordinal); err != nil {
			return ValuePublication{}, err
		}
		writer.ordinals[source] = ordinal
	}
	if err := writer.Append(ctx, "$operationalValueEvidence", row); err != nil {
		return ValuePublication{}, err
	}
	if err := writer.Flush(ctx); err != nil {
		return ValuePublication{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cao_value_publications(namespace,packet_id,digest,published_at)
		VALUES($1,$2,$3,now()) ON CONFLICT(namespace,packet_id) DO UPDATE SET digest=excluded.digest,published_at=excluded.published_at`,
		s.namespace, result.ID, digest); err != nil {
		return ValuePublication{}, err
	}
	if err := writer.valueQuality(ctx); err != nil {
		return ValuePublication{}, err
	}
	if proof != nil {
		if err := s.replaceValueProof(ctx, tx, proof.repository, proof.workflow, proof.files, proof.updatedAt); err != nil {
			return ValuePublication{}, err
		}
	}
	capture, _ := evidenceTime(row["observedAt"])
	if err := tx.QueryRow(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2)
		WHERE namespace=$1 RETURNING revision`, s.namespace, capture).Scan(&result.Revision); err != nil {
		return ValuePublication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ValuePublication{}, err
	}
	result.Updated = true
	return result, nil
}

func (w *Writer) valueQuality(ctx context.Context) error {
	_, err := w.tx.Exec(ctx, `INSERT INTO cao_quality(namespace,collection,availability,completeness,freshness,as_of,retrieved_at)
		SELECT $1,'$operationalValueEvidence','available','unknown','unknown',min(window_end),max(observed_at)
		FROM operational_value_evidence WHERE namespace=$1 HAVING count(*)>0
		ON CONFLICT(namespace,collection) DO UPDATE SET availability=excluded.availability,
		completeness='unknown',freshness='unknown',as_of=excluded.as_of,retrieved_at=excluded.retrieved_at`, w.store.namespace)
	return err
}
