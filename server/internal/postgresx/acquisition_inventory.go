package postgresx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

var _ collectnative.InventoryStore = Acquisition{}

func (a Acquisition) PinInventoryConfiguration(ctx context.Context, snapshot collectnative.InventorySnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	p := snapshot.Provenance
	if _, err := tx.ExecContext(ctx, `INSERT INTO cao_acquisition_inventory_config
		(namespace,source_repository,source_sha,policy_sha,snapshot_digest) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(namespace) DO NOTHING`, a.store.namespace, p.SourceRepository, p.SourceSHA, p.PolicySHA, snapshot.Digest); err != nil {
		return err
	}
	if err := a.inventoryConfiguration(ctx, tx, snapshot.Digest, &p); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceInventoryConfiguration is an explicit reviewed deployment CAS, not an
// automatic refresh fallback. Old replicas immediately fail publication and
// existing inventory no longer satisfies readiness for the new configuration.
func (a Acquisition) ReplaceInventoryConfiguration(ctx context.Context, expected string, snapshot collectnative.InventorySnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	p := snapshot.Provenance
	result, err := tx.ExecContext(ctx, `UPDATE cao_acquisition_inventory_config
		SET source_repository=$3,source_sha=$4,policy_sha=$5,snapshot_digest=$6
		WHERE namespace=$1 AND snapshot_digest=$2`, a.store.namespace, expected,
		p.SourceRepository, p.SourceSHA, p.PolicySHA, snapshot.Digest)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return collectnative.ErrClaimLost
	}
	return tx.Commit()
}

func (a Acquisition) inventoryConfiguration(ctx context.Context, tx *sql.Tx, digest string, expected *collectnative.InventoryProvenance) error {
	var current collectnative.InventoryProvenance
	var currentDigest string
	err := tx.QueryRowContext(ctx, `SELECT source_repository,source_sha,policy_sha,snapshot_digest
		FROM cao_acquisition_inventory_config WHERE namespace=$1 FOR SHARE`, a.store.namespace).
		Scan(&current.SourceRepository, &current.SourceSHA, &current.PolicySHA, &currentDigest)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (currentDigest != digest || expected != nil && current != *expected) {
		return collectnative.ErrClaimLost
	}
	return err
}

func (a Acquisition) BeginInventoryRefresh(ctx context.Context, scope collectnative.EnrollmentScope, digest string) (collectnative.InventoryClaim, error) {
	claim := collectnative.InventoryClaim{Scope: scope, Digest: digest}
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return claim, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, scope); err != nil {
		return claim, err
	}
	if err := a.inventoryConfiguration(ctx, tx, digest, nil); err != nil {
		return claim, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO cao_acquisition_inventory AS i
		(namespace,repository,installation_id,epoch,generation,published_generation,snapshot_digest)
		VALUES($1,$2,$3,$4,1,0,$5) ON CONFLICT(namespace,repository) DO UPDATE SET
		installation_id=excluded.installation_id,epoch=excluded.epoch,generation=i.generation+1,
		published_generation=CASE WHEN i.epoch=excluded.epoch AND i.snapshot_digest=excluded.snapshot_digest
			THEN i.published_generation ELSE 0 END,
		content=CASE WHEN i.epoch=excluded.epoch AND i.snapshot_digest=excluded.snapshot_digest THEN i.content END,
		observed_at=CASE WHEN i.epoch=excluded.epoch AND i.snapshot_digest=excluded.snapshot_digest THEN i.observed_at END,
		snapshot_digest=excluded.snapshot_digest RETURNING generation`,
		a.store.namespace, scope.Repository, scope.InstallationID, scope.Epoch, digest).Scan(&claim.Generation)
	if err != nil {
		return claim, err
	}
	return claim, tx.Commit()
}

func (a Acquisition) PublishInventory(ctx context.Context, claim collectnative.InventoryClaim, evidence collectnative.InventoryEvidence) error {
	if err := collectnative.ValidateInventoryEvidence(evidence); err != nil {
		return err
	}
	if claim.Scope != evidence.Scope || claim.Digest != evidence.Digest || claim.Generation <= 0 {
		return collectnative.ErrIdentity
	}
	content, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, claim.Scope); err != nil {
		return err
	}
	if err := a.inventoryConfiguration(ctx, tx, claim.Digest, &evidence.Provenance); err != nil {
		return err
	}
	var generation, published int64
	err = tx.QueryRowContext(ctx, `SELECT generation,published_generation FROM cao_acquisition_inventory
		WHERE namespace=$1 AND repository=$2 AND installation_id=$3 AND epoch=$4 AND snapshot_digest=$5 FOR UPDATE`,
		a.store.namespace, claim.Scope.Repository, claim.Scope.InstallationID, claim.Scope.Epoch, claim.Digest).
		Scan(&generation, &published)
	if errors.Is(err, sql.ErrNoRows) || err == nil && generation != claim.Generation {
		return collectnative.ErrClaimLost
	}
	if err != nil {
		return err
	}
	if published == claim.Generation {
		return tx.Commit()
	}
	for _, name := range []string{"campaigns", "repositories", "workflows"} {
		for _, raw := range evidence.Sources[name].Rows {
			row, err := normalizeNativeInventory(name, raw)
			if err != nil {
				return err
			}
			if err := a.store.rebindInventoryIdentity(ctx, tx, name, row, raw); err != nil {
				return err
			}
			if err := a.store.upsertNativeInventory(ctx, tx, "$"+name, row, evidence.ObservedAt); err != nil {
				return err
			}
		}
		if err := a.store.inventoryQuality(ctx, tx, "$"+name, evidence.Sources[name].Metadata); err != nil {
			return err
		}
	}
	if complete, _ := evidence.Sources["workflows"].Metadata["registry-complete"].(bool); complete {
		paths := make([]string, 0, len(evidence.Sources["workflows"].Rows))
		for _, raw := range evidence.Sources["workflows"].Rows {
			path, _ := raw["workflow"].(string)
			paths = append(paths, strings.ToLower(path))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workflows SET state='unknown',registry_state='unknown'
			WHERE namespace=$1 AND repository_id IN (SELECT id FROM repositories
				WHERE namespace=$1 AND lower(owner || '/' || name)=$2)
			AND NOT (lower(path)=ANY($3::text[]))`,
			a.store.namespace, claim.Scope.Repository, paths); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cao_acquisition_inventory SET content=$6,observed_at=$7,published_generation=generation
		WHERE namespace=$1 AND repository=$2 AND installation_id=$3 AND epoch=$4 AND generation=$5`,
		a.store.namespace, claim.Scope.Repository, claim.Scope.InstallationID, claim.Scope.Epoch, claim.Generation, content, evidence.ObservedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2)
		WHERE namespace=$1`, a.store.namespace, evidence.ObservedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (a Acquisition) EnsureInventoryReady(ctx context.Context, scope collectnative.EnrollmentScope, digest string) error {
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, scope); err != nil {
		return err
	}
	if err := a.inventoryReady(ctx, tx, scope, digest); err != nil {
		return err
	}
	return tx.Commit()
}

// inventoryReady is the opt-in activation gate. The caller must already hold
// the canonical writer and current run-scope lock in this transaction.
func (a Acquisition) inventoryReady(ctx context.Context, tx *sql.Tx, scope collectnative.EnrollmentScope, digest string) error {
	if err := a.inventoryConfiguration(ctx, tx, digest, nil); err != nil {
		return err
	}
	if err := a.requireInventoryReady(ctx, tx, scope); err != nil {
		return err
	}
	var content []byte
	if err := tx.QueryRowContext(ctx, `SELECT content FROM cao_acquisition_inventory
		WHERE namespace=$1 AND repository=$2 AND snapshot_digest=$3`,
		a.store.namespace, scope.Repository, digest).Scan(&content); err != nil {
		return err
	}
	var evidence collectnative.InventoryEvidence
	if err := decodeJSON(content, &evidence); err != nil {
		return err
	}
	snapshot, err := collectnative.NewInventorySnapshot(evidence.SourceSnapshot, digest, evidence.Provenance)
	if err != nil {
		return err
	}
	control := collectnative.EnrollmentScope{Repository: snapshot.ControlRepository()}
	err = tx.QueryRowContext(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
		WHERE namespace=$1 AND repository=$2 AND installation_id>0 FOR SHARE`,
		a.store.namespace, control.Repository).Scan(&control.InstallationID, &control.Epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("native inventory control repository is not currently enrolled")
	}
	if err != nil {
		return err
	}
	if control != scope {
		if err := a.requireInventoryReady(ctx, tx, control); err != nil {
			return fmt.Errorf("native inventory control source is not ready: %w", err)
		}
	}
	var source struct {
		Sources map[string]model.Source `json:"sources"`
	}
	if err := decodeJSON(evidence.SourceSnapshot, &source); err != nil {
		return err
	}
	declaredCampaigns := map[string]bool{}
	for _, row := range source.Sources["campaigns"].Rows {
		slug, _ := row["campaign"].(string)
		declaredCampaigns[slug] = true
	}
	paths, campaigns := []string{}, []string{}
	for _, row := range source.Sources["workflows"].Rows {
		owner, _ := row["organization"].(string)
		name, _ := row["repository"].(string)
		campaign, _ := row["campaign"].(string)
		if strings.ToLower(owner+"/"+name) == control.Repository && campaign != "" && declaredCampaigns[campaign] {
			normalized, err := normalizeNativeInventory("workflows", row)
			if err != nil {
				return err
			}
			paths = append(paths, normalized["path"].(string))
			campaigns = append(campaigns, campaign)
		}
	}
	var available bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workflows w
		JOIN repositories r ON r.namespace=w.namespace AND r.id=w.repository_id
		JOIN campaigns c ON c.namespace=w.namespace AND c.id=w.campaign_id
		JOIN unnest($3::text[],$4::text[]) declared(path,campaign)
			ON lower(w.path)=declared.path AND c.slug=declared.campaign
		WHERE w.namespace=$1 AND lower(r.owner || '/' || r.name)=$2
		)`, a.store.namespace, control.Repository, paths, campaigns).Scan(&available); err != nil {
		return err
	}
	if !available {
		return errors.New("native inventory canonical control campaign workflow is missing")
	}
	return nil
}

// Call under the same canonical writer + scope lock as run activation. A
// separate readiness precheck is not an activation fence.
func (a Acquisition) requireInventoryReady(ctx context.Context, tx *sql.Tx, scope collectnative.EnrollmentScope) error {
	var content []byte
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT i.content,i.snapshot_digest FROM cao_acquisition_inventory i
		JOIN cao_acquisition_inventory_config c ON c.namespace=i.namespace AND c.snapshot_digest=i.snapshot_digest
		WHERE i.namespace=$1 AND i.repository=$2 AND i.installation_id=$3 AND i.epoch=$4
		AND i.published_generation>0 AND i.content IS NOT NULL`,
		a.store.namespace, scope.Repository, scope.InstallationID, scope.Epoch).Scan(&content, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("native inventory is not ready for current committed scope and reviewed source")
	}
	if err != nil {
		return err
	}
	var evidence collectnative.InventoryEvidence
	if len(content) > collectnative.InventoryMaxBytes || decodeJSON(content, &evidence) != nil ||
		evidence.Scope != scope || evidence.Digest != digest || collectnative.ValidateInventoryEvidence(evidence) != nil {
		return errors.New("native inventory retained evidence is invalid")
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE namespace=$1)`,
		a.store.namespace).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("native inventory canonical campaigns are missing")
	}
	return nil
}

// InventoryEvidence returns the scoped retained source envelopes, including
// configuration-policy diagnostics that are not canonical entity collections.
func (a Acquisition) InventoryEvidence(ctx context.Context, scope collectnative.EnrollmentScope) (collectnative.InventoryEvidence, error) {
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return collectnative.InventoryEvidence{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, scope); err != nil {
		return collectnative.InventoryEvidence{}, err
	}
	if err := a.requireInventoryReady(ctx, tx, scope); err != nil {
		return collectnative.InventoryEvidence{}, err
	}
	var content []byte
	if err := tx.QueryRowContext(ctx, `SELECT content FROM cao_acquisition_inventory
		WHERE namespace=$1 AND repository=$2`, a.store.namespace, scope.Repository).Scan(&content); err != nil {
		return collectnative.InventoryEvidence{}, err
	}
	var evidence collectnative.InventoryEvidence
	if err := decodeJSON(content, &evidence); err != nil {
		return evidence, err
	}
	return evidence, tx.Commit()
}

func (a Acquisition) PendingInventoryScopes(ctx context.Context, limit int) ([]collectnative.EnrollmentScope, error) {
	if limit < 1 || limit > 256 {
		return nil, collectnative.ErrCapacity
	}
	rows, err := a.store.db.QueryContext(ctx, `SELECT i.repository,s.installation_id,s.epoch
		FROM cao_acquisition_inventory i JOIN cao_acquisition_scopes s ON s.namespace=i.namespace AND s.repository=i.repository
		JOIN cao_acquisition_inventory_config c ON c.namespace=i.namespace
		WHERE i.namespace=$1 AND s.installation_id>0
		AND (i.generation>i.published_generation OR i.epoch<>s.epoch OR i.snapshot_digest<>c.snapshot_digest)
		ORDER BY i.repository LIMIT $2`, a.store.namespace, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var scopes []collectnative.EnrollmentScope
	for rows.Next() {
		var scope collectnative.EnrollmentScope
		if err := rows.Scan(&scope.Repository, &scope.InstallationID, &scope.Epoch); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

func normalizeNativeInventory(name string, input model.Row) (model.Row, error) {
	if name == "campaigns" && input["campaign-intelligence-declaration"] != nil {
		// The current typed PostgreSQL schema has no lossless declaration
		// envelope. Do not silently drop it and claim inventory readiness.
		return nil, fmt.Errorf("%w: native campaign intelligence declaration storage", collectnative.ErrUnsupported)
	}
	row, err := NormalizeInventoryFields(name, input)
	if err != nil {
		return nil, err
	}
	text := func(field string) string { value, _ := input[field].(string); return strings.TrimSpace(value) }
	if observed, exists := input["observed-at"]; exists {
		row["observedAt"] = observed
	}
	switch name {
	case "campaigns":
		slug := text("campaign")
		row["id"], row["slug"] = "campaign:dashboard-sources:"+model.EncodeCoordinate(slug), slug
		if _, exists := row["name"]; !exists {
			row["name"] = slug
		}
	case "repositories":
		coordinate := strings.ToLower(text("organization") + "/" + text("repository"))
		row["id"], row["owner"], row["name"] = "repository:"+model.EncodeCoordinate(coordinate), text("organization"), text("repository")
	case "workflows":
		coordinate := strings.ToLower(text("organization") + "/" + text("repository"))
		path := strings.ToLower(text("workflow"))
		if strings.HasSuffix(path, ".lock.yml") {
			path = strings.TrimSuffix(path, ".lock.yml") + ".md"
		}
		row["id"], row["repositoryId"], row["path"] = "workflow:"+model.EncodeCoordinate(coordinate+":"+path), "repository:"+model.EncodeCoordinate(coordinate), path
		campaign := text("campaign")
		if campaign == "" {
			campaign = text("package")
		}
		if campaign != "" {
			row["campaignId"] = "campaign:dashboard-sources:" + model.EncodeCoordinate(campaign)
		}
		if _, exists := row["name"]; !exists {
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
	default:
		return nil, errors.New("unsupported native inventory collection")
	}
	return row, nil
}

func (s *Store) rebindInventoryIdentity(ctx context.Context, tx *sql.Tx, name string, row, raw model.Row) error {
	var err error
	var id string
	switch name {
	case "campaigns":
		err = tx.QueryRowContext(ctx, `SELECT id FROM campaigns WHERE namespace=$1 AND slug=$2 ORDER BY id LIMIT 1`,
			s.namespace, row["slug"]).Scan(&id)
	case "repositories":
		err = tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE namespace=$1 AND lower(owner)=lower($2) AND lower(name)=lower($3)
			ORDER BY (id=$4) DESC,id LIMIT 1`, s.namespace, row["owner"], row["name"], row["id"]).Scan(&id)
	case "workflows":
		// Repository identities may have originated in a different ingestion
		// adapter; match its exact canonical parent instead of duplicating it.
		err = tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE namespace=$1 AND lower(owner)=lower($2) AND lower(name)=lower($3)
			ORDER BY (id=$4) DESC,id LIMIT 1`, s.namespace, raw["organization"], raw["repository"], row["repositoryId"]).Scan(&id)
		if err != nil {
			return err
		}
		row["repositoryId"] = id
		if campaignID, exists := row["campaignId"]; exists {
			err = tx.QueryRowContext(ctx, `SELECT id FROM campaigns WHERE namespace=$1 AND (id=$2 OR slug=$3 OR slug=$4)
				ORDER BY (id=$2) DESC,id LIMIT 1`, s.namespace, campaignID, raw["campaign"], raw["package"]).Scan(&id)
			if err != nil {
				return err
			}
			row["campaignId"] = id
		}
		err = tx.QueryRowContext(ctx, `SELECT id FROM workflows WHERE namespace=$1 AND repository_id=$2 AND lower(path)=lower($3)
			ORDER BY (id=$4) DESC,id LIMIT 1`, s.namespace, row["repositoryId"], row["path"], row["id"]).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err == nil {
		row["id"] = id
	}
	return err
}

func (s *Store) upsertNativeInventory(ctx context.Context, tx *sql.Tx, source string, row model.Row, now time.Time) error {
	if _, exists := row["observedAt"]; !exists {
		row["observedAt"] = now
	}
	if err := insertInvocationEntity(ctx, tx, s.namespace, source, row, now); err != nil {
		return err
	}
	table := entityTables[source]
	args := []any{s.namespace, row["id"], stringPresence(table, row)}
	updates := []string{"present_fields=present_fields | $3::bit varying"}
	for index, column := range table.columns {
		value, present := entityFieldValue(row, column.field)
		if !present || value == nil || column.field == "id" {
			continue
		}
		bound, err := column.bind(value)
		if err != nil {
			return err
		}
		args = append(args, bound)
		name := query.SQLIdentifier(column.name)
		identity := source == "$repositories" && (column.field == "owner" || column.field == "name") ||
			source == "$workflows" && (column.field == "repositoryId" || column.field == "path") ||
			source == "$campaigns" && column.field == "slug"
		if identity {
			updates = append(updates, fmt.Sprintf("%s=CASE WHEN get_bit(present_fields,%d)=0 THEN $%d ELSE %s END", name, index, len(args), name))
		} else {
			updates = append(updates, fmt.Sprintf("%s=$%d", name, len(args)))
		}
	}
	statement, _, err := sqlbuilder.Build("UPDATE {} SET {} WHERE namespace=$1 AND id=$2",
		sqlbuilder.Identifier(table.name), sqlbuilder.Fragment(strings.Join(updates, ",")))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}

func (s *Store) inventoryQuality(ctx context.Context, tx *sql.Tx, source string, metadata model.Metadata) error {
	value := func(field, fallback string) string {
		text, ok := metadata[field].(string)
		if !ok || text == "" {
			return fallback
		}
		return text
	}
	freshness := value("freshness", "unknown")
	if freshness == "fresh" {
		freshness = "current"
	}
	var asOf, retrieved any
	for _, field := range []string{"as-of", "retrieved-at"} {
		text := value(field, "")
		if field == "as-of" && text == "" {
			text = value("observed-at", "")
		}
		if text == "" {
			continue
		}
		instant, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return err
		}
		if field == "as-of" {
			asOf = instant
		} else {
			retrieved = instant
		}
	}
	// This is one repository's evidence, never proof of fleet completeness.
	_, err := tx.ExecContext(ctx, `INSERT INTO cao_quality(namespace,collection,availability,completeness,freshness,as_of,retrieved_at)
		VALUES($1,$2,$3,'partial',$4,$5,$6) ON CONFLICT(namespace,collection) DO UPDATE SET
		availability=CASE WHEN cao_quality.availability='available' OR excluded.availability='available' THEN 'available' ELSE 'unavailable' END,
		completeness='partial',freshness=excluded.freshness,as_of=excluded.as_of,retrieved_at=excluded.retrieved_at`,
		s.namespace, source, value("availability", "available"), freshness, asOf, retrieved)
	return err
}
