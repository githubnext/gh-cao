package collectnative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const InventoryMaxBytes = 16 << 20
const InventoryMaxRows = 16384

var inventorySHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var inventoryDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// InventoryProvenance is deployment-reviewed observability input, not execution
// authority. PolicySHA identifies the revision accepted by the policy resolver.
type InventoryProvenance struct {
	SourceRepository string `json:"sourceRepository"`
	SourceSHA        string `json:"sourceSHA"`
	PolicySHA        string `json:"policySHA"`
}

func (p InventoryProvenance) Validate() error {
	if !coordinate.MatchString(p.SourceRepository) || len(p.SourceRepository) > 256 ||
		!inventorySHA.MatchString(p.SourceSHA) || !inventorySHA.MatchString(p.PolicySHA) {
		return ErrIdentity
	}
	return nil
}

// InventorySnapshot contains the complete reviewed Activity inventory source
// envelopes, including configuration-policy and maintenance/readiness fields.
// Construct it from immutable bytes at the deployment boundary, never from a
// target repository's claimed policy. Private bytes prevent caller mutation.
type InventorySnapshot struct {
	Provenance        InventoryProvenance
	Digest            string
	content           []byte
	controlRepository string
}

type inventorySnapshotDocument struct {
	InventoryProvenance
	Version           int                     `json:"version"`
	ControlRepository string                  `json:"controlRepository"`
	PolicyResolution  string                  `json:"policyResolution"`
	Sources           map[string]model.Source `json:"sources"`
}

func NewInventorySnapshot(content []byte, expected string, provenance InventoryProvenance) (InventorySnapshot, error) {
	if err := provenance.Validate(); err != nil {
		return InventorySnapshot{}, err
	}
	if len(content) == 0 || len(content) > InventoryMaxBytes {
		return InventorySnapshot{}, ErrCapacity
	}
	sum := sha256.Sum256(content)
	if !inventoryDigest.MatchString(expected) || hex.EncodeToString(sum[:]) != expected {
		return InventorySnapshot{}, errors.New("native inventory snapshot digest mismatch")
	}
	var document inventorySnapshotDocument
	if err := decode(content, &document); err != nil || document.Version != 1 || document.InventoryProvenance != provenance {
		return InventorySnapshot{}, errors.New("invalid source-bound native inventory snapshot")
	}
	if !coordinate.MatchString(document.ControlRepository) || len(document.ControlRepository) > 256 {
		return InventorySnapshot{}, errors.New("native inventory requires an explicit control repository")
	}
	if document.PolicyResolution != "available" && document.PolicyResolution != "unavailable" {
		return InventorySnapshot{}, errors.New("native inventory requires explicit policy resolution evidence")
	}
	count := 0
	for name, source := range document.Sources {
		switch name {
		case "campaigns", "repositories", "workflows", "configuration-policy", "marketplace-packages", "marketplace-registries":
		default:
			return InventorySnapshot{}, errors.New("native inventory snapshot has an unsupported source")
		}
		if source.Source != "" && source.Source != name {
			return InventorySnapshot{}, ErrIdentity
		}
		count += len(source.Rows)
		for _, row := range source.Rows {
			if row == nil {
				return InventorySnapshot{}, errors.New("native inventory row must be an object")
			}
		}
	}
	if count > InventoryMaxRows {
		return InventorySnapshot{}, ErrCapacity
	}
	for _, name := range []string{"campaigns", "repositories", "workflows", "configuration-policy"} {
		if _, exists := document.Sources[name]; !exists {
			return InventorySnapshot{}, fmt.Errorf("native inventory snapshot is missing %s", name)
		}
	}
	if len(document.Sources["campaigns"].Rows) == 0 || len(document.Sources["configuration-policy"].Rows) != 1 {
		return InventorySnapshot{}, errors.New("native inventory requires campaigns and reviewed configuration-policy")
	}
	for _, row := range document.Sources["campaigns"].Rows {
		if inventoryText(row, "campaign") == "" {
			return InventorySnapshot{}, errors.New("native inventory campaign requires a slug")
		}
	}
	policy := document.Sources["configuration-policy"].Rows[0]
	if inventoryText(policy, "path") != ".github/workflows/cao.json" {
		return InventorySnapshot{}, errors.New("native inventory configuration-policy path mismatch")
	}
	return InventorySnapshot{Provenance: provenance, Digest: expected, content: bytes.Clone(content),
		controlRepository: document.ControlRepository}, nil
}

func (s InventorySnapshot) ControlRepository() string { return s.controlRepository }

// Bytes returns a copy of the exact reviewed document, preserving its digest
// across deployment provisioning and replica restarts.
func (s InventorySnapshot) Bytes() []byte { return bytes.Clone(s.content) }

func (s InventorySnapshot) document() (inventorySnapshotDocument, error) {
	// Recheck exported provenance/digest too: mutation must not change the
	// deployment identity without changing and revalidating its immutable bytes.
	validated, err := NewInventorySnapshot(s.content, s.Digest, s.Provenance)
	if err != nil {
		return inventorySnapshotDocument{}, err
	}
	var document inventorySnapshotDocument
	err = decode(validated.content, &document)
	return document, err
}

func (s InventorySnapshot) Validate() error {
	document, err := s.document()
	if err != nil {
		return err
	}
	if document.PolicyResolution != "available" {
		return errors.New("native inventory reviewed policy resolution is unavailable")
	}
	diagnostics, ok := document.Sources["configuration-policy"].Rows[0]["diagnostics"].([]any)
	if !ok || len(diagnostics) == 0 {
		return errors.New("native inventory reviewed policy diagnostics are missing")
	}
	for _, diagnostic := range diagnostics {
		entry, ok := diagnostic.(map[string]any)
		if !ok || entry["severity"] != "valid" {
			return errors.New("native inventory reviewed policy diagnostics are not valid")
		}
	}
	campaigns := map[string]bool{}
	for _, campaign := range document.Sources["campaigns"].Rows {
		campaigns[inventoryText(campaign, "campaign")] = true
	}
	hasControlCampaign := false
	for _, workflow := range document.Sources["workflows"].Rows {
		repository := strings.ToLower(inventoryText(workflow, "organization") + "/" + inventoryText(workflow, "repository"))
		if repository == document.ControlRepository && campaigns[inventoryText(workflow, "campaign")] {
			hasControlCampaign = true
		}
	}
	if !hasControlCampaign {
		return errors.New("native inventory has no source-bound control campaign workflow")
	}
	return nil
}

// InventoryEvidence retains unprojected envelopes as well as canonical parents.
// Unresolved registry/version evidence stays partial; it never becomes a grant.
type InventoryEvidence struct {
	Provenance     InventoryProvenance     `json:"provenance"`
	Digest         string                  `json:"snapshotDigest"`
	SourceSnapshot []byte                  `json:"sourceSnapshot"`
	Scope          EnrollmentScope         `json:"scope"`
	ObservedAt     time.Time               `json:"observedAt"`
	Sources        map[string]model.Source `json:"sources"`
}

type InventoryProvider interface {
	BuildInventory(context.Context, EnrollmentScope, InventorySnapshot) (InventoryEvidence, error)
}

type InventoryClaim struct {
	Scope      EnrollmentScope
	Digest     string
	Generation int64
}

// InventoryStore admits refresh intent durably before acquisition. Publication
// must fence all three of committed scope, deployment digest and generation in
// the same transaction that writes parents, quality and canonical revision.
type InventoryStore interface {
	PinInventoryConfiguration(context.Context, InventorySnapshot) error
	BeginInventoryRefresh(context.Context, EnrollmentScope, string) (InventoryClaim, error)
	PublishInventory(context.Context, InventoryClaim, InventoryEvidence) error
	EnsureInventoryReady(context.Context, EnrollmentScope, string) error
	PendingInventoryScopes(context.Context, int) ([]EnrollmentScope, error)
}

func inventoryText(row model.Row, field string) string {
	text, _ := row[field].(string)
	return strings.TrimSpace(text)
}

func inventoryPath(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !strings.HasPrefix(value, ".github/workflows/") || path.Clean(value) != value ||
		strings.Contains(strings.TrimPrefix(value, ".github/workflows/"), "/") {
		return ""
	}
	if strings.HasSuffix(value, ".lock.yml") {
		value = strings.TrimSuffix(value, ".lock.yml") + ".md"
	}
	return value
}

// ValidateInventoryEvidence also protects direct store callers, not just the
// provider/service. Repository scope comes only from committed enrollment.
func ValidateInventoryEvidence(e InventoryEvidence) error {
	if e.Provenance.Validate() != nil || !inventoryDigest.MatchString(e.Digest) ||
		(Identity{Repository: e.Scope.Repository, InstallationID: e.Scope.InstallationID, RunID: 1, Attempt: 1}).Validate() != nil ||
		e.Scope.Epoch <= 0 || e.ObservedAt.IsZero() {
		return ErrIdentity
	}
	snapshot, err := NewInventorySnapshot(e.SourceSnapshot, e.Digest, e.Provenance)
	if err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	document, err := snapshot.document()
	if err != nil {
		return err
	}
	for _, name := range []string{"campaigns", "configuration-policy", "marketplace-packages", "marketplace-registries"} {
		actual, _ := json.Marshal(e.Sources[name].Rows)
		expected, _ := json.Marshal(document.Sources[name].Rows)
		if !bytes.Equal(actual, expected) {
			return errors.New("native inventory changed reviewed source declarations")
		}
	}
	count := 0
	for name, source := range e.Sources {
		if _, ok := document.Sources[name]; !ok {
			return errors.New("native inventory added an unsupported source")
		}
		if source.Source != "" && source.Source != name {
			return ErrIdentity
		}
		count += len(source.Rows)
		for _, row := range source.Rows {
			if row == nil {
				return errors.New("native inventory row must be an object")
			}
		}
	}
	if count > InventoryMaxRows {
		return ErrCapacity
	}
	if len(e.Sources["campaigns"].Rows) == 0 || len(e.Sources["repositories"].Rows) != 1 ||
		len(e.Sources["configuration-policy"].Rows) != 1 {
		return errors.New("native inventory has no valid source-bound parents")
	}
	campaigns := map[string]bool{}
	for _, row := range e.Sources["campaigns"].Rows {
		slug := inventoryText(row, "campaign")
		if slug == "" || campaigns[slug] {
			return errors.New("invalid or duplicate native inventory campaign")
		}
		campaigns[slug] = true
	}
	seen := map[string]bool{}
	for _, name := range []string{"repositories", "workflows"} {
		for _, row := range e.Sources[name].Rows {
			repository := strings.ToLower(inventoryText(row, "organization") + "/" + inventoryText(row, "repository"))
			if repository != e.Scope.Repository {
				return ErrIdentity
			}
			if name == "workflows" {
				key := inventoryPath(inventoryText(row, "workflow"))
				if key == "" || seen[key] {
					return errors.New("invalid or duplicate native inventory workflow")
				}
				seen[key] = true
				campaign := inventoryText(row, "campaign")
				if campaign == "" {
					campaign = inventoryText(row, "package")
				}
				if campaign != "" && !campaigns[campaign] {
					return errors.New("native inventory workflow campaign is missing")
				}
			}
		}
	}
	declaredByPath := map[string]model.Row{}
	for _, declared := range document.Sources["workflows"].Rows {
		repository := strings.ToLower(inventoryText(declared, "organization") + "/" + inventoryText(declared, "repository"))
		if repository == e.Scope.Repository {
			key := inventoryPath(inventoryText(declared, "workflow"))
			if key == "" || declaredByPath[key] != nil {
				return errors.New("invalid or duplicate reviewed native workflow")
			}
			declaredByPath[key] = declared
		}
	}
	mutable := map[string]bool{"workflow": true, "workflow-name": true, "workflow-active": true,
		"workflow-registry-state": true, "workflow-id": true, "workflow-link": true, "created-at": true, "updated-at": true,
		"gh-aw-version": true, "gh-aw-current-version": true, "gh-aw-update-state": true, "observed-at": true}
	for _, observed := range e.Sources["workflows"].Rows {
		key := inventoryPath(inventoryText(observed, "workflow"))
		declared := declaredByPath[key]
		if declared == nil {
			if inventoryText(observed, "workflow-role") != "standalone" ||
				inventoryText(observed, "workflow-source-kind") != "registry" || inventoryText(observed, "campaign") != "" ||
				inventoryText(observed, "package") != "" || inventoryText(observed, "rollout-mode") != "unknown" {
				return errors.New("remote workflow metadata cannot grant campaign authority")
			}
			for field := range observed {
				if !mutable[field] && field != "organization" && field != "repository" &&
					field != "workflow-role" && field != "workflow-source-kind" && field != "rollout-mode" {
					return errors.New("remote workflow metadata cannot add reviewed declarations")
				}
			}
			continue
		}
		for field := range observed {
			if _, reviewed := declared[field]; !reviewed && !mutable[field] {
				return errors.New("native inventory added unreviewed workflow declarations")
			}
		}
		for field, expected := range declared {
			if mutable[field] {
				continue
			}
			left, _ := json.Marshal(observed[field])
			right, _ := json.Marshal(expected)
			if !bytes.Equal(left, right) {
				return errors.New("native inventory changed reviewed workflow declarations")
			}
		}
		delete(declaredByPath, key)
	}
	if len(declaredByPath) > 0 {
		return errors.New("native inventory lost reviewed workflow declarations")
	}
	encoded, err := json.Marshal(e)
	if err != nil || len(encoded) > InventoryMaxBytes {
		return ErrCapacity
	}
	return nil
}
