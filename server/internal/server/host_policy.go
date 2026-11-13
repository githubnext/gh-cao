package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultHostPolicyPath = ".github/workflows/cao.json"
	maxHostPolicyBytes    = 4 << 20
	maxHostProfileDepth   = 8
)

var environmentVariablePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

type resolvedHostPolicy struct {
	Profile                HostProfile
	SingleReplicaConfirmed bool
	OperationalBackend     string
	DatabaseNamespace      string
	OperationalNamespace   string
	AllowVolatile          bool
	OperationalPostgresURL string
	CORS                   CORSPolicy
}

type hostPolicyDocument struct {
	ControlPlane struct {
		Web struct {
			Host json.RawMessage `json:"host"`
		} `json:"web"`
	} `json:"control-plane"`
}

type hostPolicy struct {
	Target      targetPolicy       `json:"target"`
	Operational *operationalPolicy `json:"operational-store"`
	CORS        corsPolicyDocument `json:"cors"`
}

type operationalPolicy struct {
	Backend       string          `json:"backend"`
	SingleProcess bool            `json:"single-process"`
	AllowVolatile bool            `json:"allow-volatile"`
	NamespaceEnv  string          `json:"namespace-env"`
	Postgres      *postgresPolicy `json:"postgres"`
}

type postgresPolicy struct {
	URLEnv string `json:"url-env"`
}

type targetPolicy struct {
	Module                string             `json:"module"`
	Name                  string             `json:"name"`
	Authentication        HostAuthentication `json:"authentication"`
	Listener              HostListener       `json:"listener"`
	RequireHTTPS          *bool              `json:"require-https"`
	TrustPlatformProxy    *bool              `json:"trust-platform-proxy"`
	SupportsSingleReplica *bool              `json:"supports-single-replica"`
	Replicas              int                `json:"replicas"`
}

func configuredHostPolicyPath() string {
	if value := strings.TrimSpace(os.Getenv("CAO_POLICY_PATH")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv(marketplacePolicyPathEnv)); value != "" {
		return value
	}
	return defaultHostPolicyPath
}

func loadHostPolicyFromEnv() (*resolvedHostPolicy, error) {
	path := configuredHostPolicyPath()
	document, err := loadComposedHostPolicy(path)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, errors.New("compose CAO host policy")
	}
	var policyDocument hostPolicyDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&policyDocument); err != nil {
		return nil, errors.New("parse CAO host policy")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("parse CAO host policy")
	}
	if len(policyDocument.ControlPlane.Web.Host) == 0 ||
		string(policyDocument.ControlPlane.Web.Host) == "null" {
		return nil, errors.New("cao.json requires control-plane.web.host")
	}
	var policy hostPolicy
	var hostFields map[string]json.RawMessage
	if err := json.Unmarshal(policyDocument.ControlPlane.Web.Host, &hostFields); err != nil {
		return nil, errors.New("parse CAO host policy")
	}
	if selector, selected := hostFields["operational-store"]; selected {
		if string(selector) == "null" {
			return nil, errors.New("operational-store must be an object")
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(selector, &fields); err != nil {
			return nil, errors.New("operational-store must be an object")
		}

		if nestedPostgres, exists := fields["postgres"]; exists {
			var postgresFields map[string]json.RawMessage
			if string(nestedPostgres) == "null" || json.Unmarshal(nestedPostgres, &postgresFields) != nil {
				return nil, errors.New("operational-store.postgres must be an object")
			}
			if reference, exists := postgresFields["url-env"]; exists {
				var name string
				if json.Unmarshal(reference, &name) != nil || !environmentVariablePattern.MatchString(name) {
					return nil, errors.New("postgres url-env must name an environment variable")
				}
			}
		}
	}
	hostDecoder := json.NewDecoder(strings.NewReader(string(policyDocument.ControlPlane.Web.Host)))
	hostDecoder.DisallowUnknownFields()
	if err := hostDecoder.Decode(&policy); err != nil {
		return nil, errors.New("parse CAO host policy")
	}

	return policy.resolve(os.LookupEnv)
}

func loadComposedHostPolicy(path string) (map[string]any, error) {
	entryPath, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve CAO host policy")
	}
	entryPath, err = filepath.EvalSymlinks(entryPath)
	if err != nil {
		return nil, errors.New("read CAO host policy")
	}
	rootDirectory := filepath.Dir(entryPath)
	loading := map[string]bool{}

	var load func(string, int) (map[string]any, error)
	load = func(currentPath string, depth int) (map[string]any, error) {
		if depth > maxHostProfileDepth {
			return nil, fmt.Errorf("deployment profile import depth exceeds %d", maxHostProfileDepth)
		}
		canonicalPath, err := filepath.EvalSymlinks(currentPath)
		if err != nil {
			return nil, errors.New("read CAO host policy")
		}
		relativePath, err := filepath.Rel(rootDirectory, canonicalPath)
		if err != nil || relativePath == ".." ||
			strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(relativePath) {
			return nil, errors.New("deployment profile imports must remain within the policy directory")
		}
		if loading[canonicalPath] {
			return nil, errors.New("deployment profile import cycle")
		}
		loading[canonicalPath] = true
		defer delete(loading, canonicalPath)

		// #nosec G304,G703 -- the operator explicitly configures the reviewed policy path,
		// and imported paths are constrained to its directory.
		data, err := os.ReadFile(canonicalPath)
		if err != nil {
			return nil, errors.New("read CAO host policy")
		}
		if len(data) > maxHostPolicyBytes {
			return nil, errors.New("CAO host policy exceeds 4 MiB")
		}
		document, err := decodeUniqueJSONObject(data)
		if err != nil {
			return nil, errors.New("parse CAO host policy")
		}
		extendsValue, extends := document["extends"]
		if !extends {
			return document, nil
		}
		if err := validateDeploymentProfileDocument(document); err != nil {
			return nil, err
		}
		extendsPath, ok := extendsValue.(string)
		if !ok || strings.TrimSpace(extendsPath) == "" || filepath.IsAbs(extendsPath) {
			return nil, errors.New("deployment profile extends must be a non-empty relative path")
		}
		base, err := load(filepath.Join(filepath.Dir(canonicalPath), extendsPath), depth+1)
		if err != nil {
			return nil, err
		}
		delete(document, "extends")
		return mergePolicyMaps(base, document), nil
	}

	return load(entryPath, 0)
}

func validateDeploymentProfileDocument(document map[string]any) error {
	if err := requireOnlyKeys(document, "extends", "control-plane"); err != nil {
		return errors.New("deployment profile may only extend control-plane.web.host")
	}
	if _, ok := document["extends"]; !ok {
		return errors.New("deployment profile requires extends and control-plane.web.host")
	}
	control, ok := document["control-plane"].(map[string]any)
	if !ok || requireOnlyKeys(control, "web") != nil {
		return errors.New("deployment profile may only extend control-plane.web.host")
	}
	if _, ok := control["web"]; !ok {
		return errors.New("deployment profile requires control-plane.web.host")
	}
	web, ok := control["web"].(map[string]any)
	if !ok || requireOnlyKeys(web, "host") != nil {
		return errors.New("deployment profile may only extend control-plane.web.host")
	}
	if _, ok := web["host"]; !ok {
		return errors.New("deployment profile requires control-plane.web.host")
	}
	return nil
}

func requireOnlyKeys(document map[string]any, allowed ...string) error {
	keys := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		keys[key] = true
	}
	for key := range document {
		if !keys[key] {
			return fmt.Errorf("unsupported key %q", key)
		}
	}
	return nil
}

func mergePolicyMaps(base, overlay map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		baseMap, baseIsMap := merged[key].(map[string]any)
		overlayMap, overlayIsMap := value.(map[string]any)
		if baseIsMap && overlayIsMap {
			merged[key] = mergePolicyMaps(baseMap, overlayMap)
		} else {
			merged[key] = value
		}
	}
	return merged
}

func decodeUniqueJSONObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("policy root must be an object")
	}
	return document, nil
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		document := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key must be a string")
			}
			if _, exists := document[key]; exists {
				return nil, fmt.Errorf("duplicate mapping key: %s", key)
			}
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			document[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return document, nil
	case '[':
		values := []any{}
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return values, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (policy hostPolicy) resolve(lookup func(string) (string, bool)) (*resolvedHostPolicy, error) {
	profile, supportsSingleReplica, err := resolveTargetModule(policy.Target)
	if err != nil {
		return nil, err
	}

	if policy.Operational == nil {
		policy.Operational = &operationalPolicy{Backend: "postgres"}
	}
	backend := policy.Operational.Backend
	switch backend {
	case "memory":
		if policy.Operational.Postgres != nil {
			return nil, errors.New("memory operational-store cannot configure PostgreSQL")
		}
		if !policy.Operational.SingleProcess || !policy.Operational.AllowVolatile {
			return nil, errors.New("memory operational-store requires single-process and allow-volatile acknowledgements")
		}
		if !supportsSingleReplica || policy.Target.Replicas != 1 || profile.Listener != HostListenerProcess {
			return nil, errors.New("memory operational-store requires an explicit single-process, single-replica process-listener target")
		}
		profile.SingleProcess = true
		profile.SingleReplica = true
		profile.SupportsCollection = true
	case "postgres":
		if policy.Operational.SingleProcess || policy.Operational.AllowVolatile {
			return nil, errors.New("postgres operational-store cannot configure memory topology")
		}
		profile.SupportsCollection = true
	default:
		return nil, fmt.Errorf("unsupported operational-store backend %q", backend)
	}
	namespace := firstNonempty(envValue(lookup, "CAO_DATASET_NAMESPACE"), "hosted-dashboard")
	operationalNamespace := firstNonempty(envValue(lookup, firstNonempty(policy.Operational.NamespaceEnv, "CAO_OPERATIONAL_NAMESPACE")), namespace)
	cors, err := policy.CORS.resolve()
	if err != nil {
		return nil, err
	}
	if backend == "memory" {
		if err := profile.validate(); err != nil {
			return nil, err
		}
		return &resolvedHostPolicy{
			Profile: profile, SingleReplicaConfirmed: true,
			OperationalBackend: backend, OperationalNamespace: operationalNamespace,
			AllowVolatile: true, DatabaseNamespace: namespace, CORS: cors,
		}, nil
	}
	if backend == "postgres" {
		if err := profile.validate(); err != nil {
			return nil, err
		}
		urlEnv := "CAO_POSTGRES_URL"
		if policy.Operational.Postgres != nil && policy.Operational.Postgres.URLEnv != "" {
			urlEnv = policy.Operational.Postgres.URLEnv
		}
		if !environmentVariablePattern.MatchString(urlEnv) {
			return nil, errors.New("postgres url-env must name an environment variable")
		}
		dsn := envValue(lookup, urlEnv)
		if dsn == "" {
			return nil, fmt.Errorf("postgres operational connection requires environment variable %s", urlEnv)
		}
		return &resolvedHostPolicy{
			Profile: profile, SingleReplicaConfirmed: true,
			OperationalBackend: backend, OperationalNamespace: operationalNamespace,
			OperationalPostgresURL: dsn, DatabaseNamespace: namespace, CORS: cors,
		}, nil
	}
	return nil, fmt.Errorf("unsupported operational-store backend %q", backend)
}

func envValue(lookup func(string) (string, bool), name string) string {
	if name == "" {
		return ""
	}
	value, _ := lookup(name)
	return strings.TrimSpace(value)
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
