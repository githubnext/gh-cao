package operationalvalue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type WorkflowPolicySnapshot struct {
	Document      json.RawMessage        `json:"document"`
	Registrations []WorkflowRegistration `json:"registrations"`
}

type WorkflowRegistration struct {
	Repository string `json:"repository"`
	Workflow   string `json:"workflow"`
	Collector  string `json:"collector"`
}

func WorkflowRegistrations(ctx context.Context, config Config, policy string) ([]WorkflowRegistration, error) {
	snapshot, err := ResolveWorkflowPolicy(ctx, config, policy)
	return snapshot.Registrations, err
}

type valuePolicy struct {
	Version int    `json:"version"`
	GHAW    string `json:"gh-aw-version"`
	Control struct {
		Scope struct {
			Owners       []string `json:"allowed-owners"`
			Repositories []string `json:"allowed-repositories"`
		} `json:"scope"`
		Activity struct {
			Repositories []string `json:"repositories"`
		} `json:"activity"`
		Campaigns map[string]struct {
			Enabled *bool               `json:"enabled"`
			Values  []valueRegistration `json:"workflow-values"`
			Workers map[string]struct {
				Workflow string `json:"workflow"`
				Enabled  *bool  `json:"enabled"`
			} `json:"workers"`
			Targets map[string]struct {
				Mode string `json:"mode"`
			} `json:"targets"`
		} `json:"campaigns"`
		Workflows map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"workflows"`
	} `json:"control-plane"`
}

type valueRegistration struct {
	Repository  string          `json:"repository"`
	Workflow    string          `json:"workflow"`
	Collector   string          `json:"collector"`
	Enabled     *bool           `json:"enabled"`
	APIRequests int             `json:"api-requests"`
	Evidence    json.RawMessage `json:"evidence"`
	Acquisition json.RawMessage `json:"acquisition"`
}

var (
	valueRepositoryPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)
	valueOwnerPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	valueWorkflowPattern    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	valueCollectorPattern   = regexp.MustCompile(`^[A-Za-z0-9._/-]+\.mjs$`)
	valueEvidencePattern    = regexp.MustCompile(`^\.github/value-evidence/[A-Za-z0-9._/-]+\.jsonl(?:\.gz)?$`)
	valueGHAWPattern        = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	valueRequirementPattern = regexp.MustCompile(`^[a-z0-9]+(?:[a-z0-9._:/-]*[a-z0-9])?$`)
)

// orderedObject preserves JSON insertion order. The policy digest is the hash
// of JSON.stringify(composedPolicy), not Go's alphabetically sorted map JSON.
type orderedObject []orderedField
type orderedField struct {
	key   string
	value any
}

func (o orderedObject) get(key string) (any, bool) {
	for _, field := range o {
		if field.key == key {
			return field.value, true
		}
	}
	return nil, false
}

func (o orderedObject) object(key string) orderedObject {
	value, _ := o.get(key)
	result, _ := value.(orderedObject)
	return result
}

func decodePolicyValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := orderedObject{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid policy key")
			}
			if _, duplicate := object.get(key); duplicate {
				return nil, fmt.Errorf("duplicate policy key %q", key)
			}
			value, err := decodePolicyValue(decoder)
			if err != nil {
				return nil, err
			}
			object = append(object, orderedField{key, value})
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		values := []any{}
		for decoder.More() {
			value, err := decodePolicyValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err := decoder.Token()
		return values, err
	}
	if text, ok := token.(string); ok && strings.Contains(text, "${{") {
		return nil, errors.New("policy must not contain GitHub Actions expressions")
	}
	return token, nil
}

func marshalPolicyValue(buffer *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case orderedObject:
		buffer.WriteByte('{')
		for i, field := range typed {
			if i != 0 {
				buffer.WriteByte(',')
			}
			key, _ := json.Marshal(field.key)
			buffer.Write(key)
			buffer.WriteByte(':')
			if err := marshalPolicyValue(buffer, field.value); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	case []any:
		buffer.WriteByte('[')
		for i, item := range typed {
			if i != 0 {
				buffer.WriteByte(',')
			}
			if err := marshalPolicyValue(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || number > 9_007_199_254_740_991 || number < -9_007_199_254_740_991 {
			return errors.New("reviewed policy number exceeds JSON safe integer precision")
		}
		buffer.WriteString(strconv.FormatFloat(number, 'f', -1, 64))
	default:
		var encoded bytes.Buffer
		writer := json.NewEncoder(&encoded)
		writer.SetEscapeHTML(false)
		err := writer.Encode(typed)
		if err != nil {
			return err
		}
		buffer.Write(bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}))
	}
	return nil
}

func composeValuePolicy(base, overlay orderedObject) orderedObject {
	result := append(orderedObject{}, base...)
	for _, field := range overlay {
		found := false
		for i := range result {
			if result[i].key == field.key {
				child, baseObject := result[i].value.(orderedObject)
				replacement, overlayObject := field.value.(orderedObject)
				if baseObject && overlayObject {
					result[i].value = composeValuePolicy(child, replacement)
				} else {
					result[i].value = field.value
				}
				found = true
				break
			}
		}
		if !found {
			result = append(result, field)
		}
	}
	return result
}

func loadValuePolicy(ctx context.Context, path string) (json.RawMessage, valuePolicy, error) {
	var policy valuePolicy
	entry, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, policy, err
	}
	root := filepath.Dir(entry)
	active := map[string]bool{}
	var load func(string, int) (orderedObject, error)
	load = func(path string, depth int) (orderedObject, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if depth > 8 {
			return nil, errors.New("deployment profile import depth exceeds 8")
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, canonical)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return nil, errors.New("deployment profile imports must remain within the policy directory")
		}
		if active[canonical] {
			return nil, errors.New("deployment profile import cycle")
		}
		active[canonical] = true
		defer delete(active, canonical)
		content, err := readBoundedValueFile(canonical, 4<<20)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		value, err := decodePolicyValue(decoder)
		if err != nil {
			return nil, err
		}
		if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
			return nil, errors.New("invalid trailing policy JSON")
		}
		document, ok := value.(orderedObject)
		if !ok {
			return nil, errors.New("policy root must be a mapping")
		}
		base, extends := document.get("extends")
		if !extends {
			return document, nil
		}
		if len(document) != 2 || document.object("control-plane") == nil ||
			len(document.object("control-plane")) != 1 ||
			len(document.object("control-plane").object("web")) != 1 {
			return nil, errors.New("deployment profile may only extend control-plane.web.host")
		}
		if _, ok := document.object("control-plane").object("web").get("host"); !ok {
			return nil, errors.New("deployment profile requires control-plane.web.host")
		}
		importPath, ok := base.(string)
		if !ok || importPath == "" || filepath.IsAbs(importPath) {
			return nil, errors.New("deployment profile extends must be a non-empty relative path")
		}
		imported, err := load(filepath.Join(filepath.Dir(canonical), importPath), depth+1)
		if err != nil {
			return nil, err
		}
		profile := orderedObject{}
		for _, field := range document {
			if field.key != "extends" {
				profile = append(profile, field)
			}
		}
		return composeValuePolicy(imported, profile), nil
	}
	document, err := load(entry, 0)
	if err != nil {
		return nil, policy, err
	}
	var buffer bytes.Buffer
	if err := marshalPolicyValue(&buffer, document); err != nil {
		return nil, policy, err
	}
	content := buffer.Bytes()
	if err := json.Unmarshal(content, &policy); err != nil {
		return nil, policy, err
	}
	if err := validateValuePolicy(document, &policy); err != nil {
		return nil, policy, err
	}
	return content, policy, nil
}

func readBoundedValueFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-selected policy or confined private transport.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, errors.New("reviewed Value input exceeds its byte budget")
	}
	return content, nil
}

func validateValuePolicy(document orderedObject, policy *valuePolicy) error {
	if policy.Version != 1 || !valueGHAWPattern.MatchString(policy.GHAW) || document.object("control-plane") == nil {
		return errors.New("value requires a reviewed version 1 control policy")
	}
	if _, ok := document.get("extends"); ok {
		return errors.New("unresolved policy profile")
	}
	if value, ok := document.get("$schema"); ok &&
		value != "https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json" {
		return errors.New("invalid reviewed policy schema")
	}
	if err := allowedPolicyKeys(document, "$schema", "version", "gh-aw-version", "control-plane", "target-authority"); err != nil {
		return err
	}
	control := document.object("control-plane")
	if err := allowedPolicyKeys(control, "scope", "activity", "inventory", "web", "defaults", "campaigns", "workflows", "operations", "publishing", "marketplace", "webhook-gateway", "token-broker"); err != nil {
		return err
	}
	for _, field := range []string{"scope", "activity", "campaigns", "workflows"} {
		if value, exists := control.get(field); exists {
			if _, ok := value.(orderedObject); !ok {
				return fmt.Errorf("reviewed control-plane.%s must be a mapping", field)
			}
		}
	}
	if err := allowedPolicyKeys(control.object("scope"), "allowed-owners", "allowed-repositories"); err != nil {
		return err
	}
	for _, field := range []string{"allowed-owners", "allowed-repositories"} {
		if value, exists := control.object("scope").get(field); exists {
			if _, ok := value.([]any); !ok {
				return errors.New("reviewed scope must be a list")
			}
		}
	}
	if _, exists := control.get("activity"); !exists || len(policy.Control.Activity.Repositories) == 0 {
		return errors.New("value requires explicit Activity repository scope")
	}
	if err := allowedPolicyKeys(control.object("activity"), "repositories", "public-only"); err != nil {
		return err
	}
	if _, ok := control.object("activity").get("repositories"); !ok {
		return errors.New("activity repositories must be explicitly reviewed")
	}
	if value, exists := control.object("activity").get("public-only"); exists {
		if _, ok := value.(bool); !ok {
			return errors.New("invalid Activity public-only setting")
		}
	}
	for _, set := range [][]string{policy.Control.Scope.Owners, policy.Control.Scope.Repositories, policy.Control.Activity.Repositories} {
		seen := map[string]bool{}
		for _, item := range set {
			key := strings.ToLower(item)
			if seen[key] {
				return errors.New("duplicate reviewed Activity scope")
			}
			seen[key] = true
		}
	}
	for _, owner := range policy.Control.Scope.Owners {
		if !valueOwnerPattern.MatchString(owner) {
			return errors.New("invalid reviewed owner scope")
		}
	}
	for _, repository := range append(append([]string{}, policy.Control.Scope.Repositories...), policy.Control.Activity.Repositories...) {
		if !valueRepositoryPattern.MatchString(repository) {
			return errors.New("invalid reviewed repository scope")
		}
	}
	for _, repository := range policy.Control.Activity.Repositories {
		if !valuePolicyInScope(policy, policy.Control.Activity.Repositories, repository) {
			return errors.New("activity repository lies outside reviewed scope")
		}
	}
	for name := range policy.Control.Campaigns {
		if !valueWorkflowPattern.MatchString(name) {
			return errors.New("invalid campaign identity")
		}
		if _, exists := control.object("campaigns").object(name).get("workflow-values"); name != "activity" && exists {
			return errors.New("workflow-values is only supported for Activity")
		}
		if enabled, exists := control.object("campaigns").object(name).get("enabled"); exists {
			if _, ok := enabled.(bool); !ok {
				return errors.New("invalid campaign enabled setting")
			}
		}
	}
	activity := policy.Control.Campaigns["activity"]
	if value, exists := control.object("campaigns").get("activity"); exists {
		if _, ok := value.(orderedObject); !ok {
			return errors.New("reviewed Activity campaign must be a mapping")
		}
	}
	if len(activity.Values) > 1000 {
		return errors.New("reviewed Value registration limit exceeded")
	}
	values, declared := control.object("campaigns").object("activity").get("workflow-values")
	items, _ := values.([]any)
	if declared && items == nil || len(items) != len(activity.Values) {
		return errors.New("invalid reviewed Value registrations")
	}
	identities := map[string]bool{}
	for i, entry := range activity.Values {
		raw, ok := items[i].(orderedObject)
		if !ok {
			return errors.New("invalid reviewed Value registration")
		}
		if err := allowedPolicyKeys(raw, "repository", "workflow", "collector", "evidence", "api-requests", "enabled", "acquisition"); err != nil {
			return err
		}
		if !valueRepositoryPattern.MatchString(entry.Repository) || !valueWorkflowPattern.MatchString(entry.Workflow) ||
			(entry.Enabled == nil || *entry.Enabled) && !valueCollectorPattern.MatchString(entry.Collector) ||
			entry.Collector != "" && (!valueCollectorPattern.MatchString(entry.Collector) || !confinedValuePath(entry.Collector)) {
			return errors.New("invalid reviewed Value registration identity or collector")
		}
		if value, exists := raw.get("enabled"); exists {
			if _, ok := value.(bool); !ok {
				return errors.New("invalid Value registration enabled flag")
			}
		}
		if value, exists := raw.get("api-requests"); exists {
			number, ok := value.(json.Number)
			if !ok || number.String() != fmt.Sprint(entry.APIRequests) || entry.APIRequests < 1 || entry.APIRequests > 1000 || entry.Collector == "" {
				return errors.New("invalid reviewed Value API budget")
			}
		}
		if len(entry.Evidence) != 0 {
			var evidence struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			}
			if err := json.Unmarshal(entry.Evidence, &evidence); err != nil {
				return err
			}
			metadata := raw.object("evidence")
			if len(metadata) != 2 || !valueEvidencePattern.MatchString(evidence.Path) ||
				!confinedValuePath(evidence.Path) ||
				(!strings.HasSuffix(evidence.Path, ".jsonl") && !strings.HasSuffix(evidence.Path, ".jsonl.gz")) ||
				!sha256Fingerprint.MatchString(evidence.SHA256) {
				return errors.New("invalid reviewed Value evidence import")
			}
		}
		if len(entry.Acquisition) != 0 && entry.Collector == "" {
			return errors.New("value acquisition requires a reviewed collector")
		}
		if len(entry.Acquisition) != 0 {
			if err := validateValueAcquisition(raw.object("acquisition")); err != nil {
				return err
			}
		}
		key := strings.ToLower(entry.Repository) + ":" + entry.Workflow
		if identities[key] {
			return errors.New("duplicate reviewed Value registration")
		}
		identities[key] = true
	}
	return nil
}

func validateValueAcquisition(acquisition orderedObject) error {
	if len(acquisition) != 1 {
		return errors.New("invalid reviewed Value acquisition manifest")
	}
	raw, exists := acquisition.get("requirements")
	requirements, ok := raw.([]any)
	if !exists || !ok || len(requirements) == 0 || len(requirements) > 64 {
		return errors.New("invalid reviewed Value acquisition requirements")
	}
	seen := map[string]bool{}
	for _, value := range requirements {
		requirement, ok := value.(orderedObject)
		if !ok || len(requirement) != 5 ||
			allowedPolicyKeys(requirement, "cache", "pool", "primitive", "scope", "source") != nil {
			return errors.New("invalid reviewed Value acquisition requirement")
		}
		fields := make([]string, 0, 5)
		for _, name := range []string{"cache", "pool", "primitive", "scope", "source"} {
			value, _ := requirement.get(name)
			text, ok := value.(string)
			if !ok || len(text) > 160 || !valueRequirementPattern.MatchString(text) {
				return errors.New("invalid reviewed Value acquisition identity")
			}
			fields = append(fields, text)
		}
		cache, pool, source := fields[0], fields[1], fields[4]
		valid := source == "canonical" && pool == "local" && cache == "canonical" ||
			source == "github" && (pool == "core" || pool == "search" || pool == "graphql") && cache == "invocation" ||
			source == "git" && pool == "git" && cache == "immutable" ||
			source == "archive" && pool == "archive" && cache == "invocation"
		if !valid {
			return errors.New("unsupported reviewed Value acquisition source or quota pool")
		}
		key := strings.Join(fields, "\x00")
		if seen[key] {
			return errors.New("duplicate reviewed Value acquisition requirement")
		}
		seen[key] = true
	}
	return nil
}

func allowedPolicyKeys(object orderedObject, allowed ...string) error {
	for _, field := range object {
		found := false
		for _, key := range allowed {
			if key == field.key {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unsupported reviewed policy field %q", field.key)
		}
	}
	return nil
}

func confinedValuePath(path string) bool {
	if filepath.IsAbs(path) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func valuePolicyInScope(policy *valuePolicy, repositories []string, repository string) bool {
	contains := func(items []string, value string) bool {
		for _, item := range items {
			if strings.EqualFold(item, value) {
				return true
			}
		}
		return false
	}
	owner, _, _ := strings.Cut(repository, "/")
	return contains(repositories, repository) &&
		contains(policy.Control.Activity.Repositories, repository) &&
		(len(policy.Control.Scope.Repositories) == 0 || contains(policy.Control.Scope.Repositories, repository)) &&
		(len(policy.Control.Scope.Owners) == 0 || contains(policy.Control.Scope.Owners, owner))
}

func valuePolicyDigest(content []byte) string {
	hash := sha256.Sum256(content)
	return fmt.Sprintf("%x", hash)
}

// ResolveWorkflowPolicy binds registrations to the same validated composed
// document. Re-resolve before publication: importing a profile never grants
// authority based solely on the unchanged entry file.
func ResolveWorkflowPolicy(ctx context.Context, _ Config, path string) (WorkflowPolicySnapshot, error) {
	document, policy, err := loadValuePolicy(ctx, path)
	if err != nil {
		return WorkflowPolicySnapshot{}, err
	}
	snapshot := WorkflowPolicySnapshot{Document: document, Registrations: []WorkflowRegistration{}}
	activity := policy.Control.Campaigns["activity"]
	if activity.Enabled != nil && !*activity.Enabled {
		return snapshot, nil
	}
	repositories := make([]string, len(activity.Values))
	for i, entry := range activity.Values {
		repositories[i] = entry.Repository
	}
	for _, entry := range activity.Values {
		if (entry.Enabled == nil || *entry.Enabled) && valuePolicyInScope(&policy, repositories, entry.Repository) {
			snapshot.Registrations = append(snapshot.Registrations, WorkflowRegistration{
				Repository: entry.Repository, Workflow: entry.Workflow, Collector: entry.Collector,
			})
		}
	}
	return snapshot, nil
}
