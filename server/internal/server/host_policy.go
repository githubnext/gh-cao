package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/namespaces"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

const (
	defaultHostPolicyPath = ".github/workflows/cao.json"
	maxHostPolicyBytes    = 4 << 20
	maxHostProfileDepth   = 8
)

var environmentVariablePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

type redisTLSMode string

const (
	redisTLSAuto     redisTLSMode = "auto"
	redisTLSRequired redisTLSMode = "required"
	redisTLSDisabled redisTLSMode = "disabled"
)

// redisTLSModeSource identifies which input supplied the resolved Redis TLS
// mode. It is useful for diagnosing a misconfigured deployment without
// logging the policy document, provider module, or resolved mode's origin
// in more detail than this classification.
type redisTLSModeSource string

const (
	redisTLSModeSourcePolicy   redisTLSModeSource = "policy"
	redisTLSModeSourceProvider redisTLSModeSource = "provider"
	redisTLSModeSourceDefault  redisTLSModeSource = "default"
)

// resolveRedisTLSMode applies the standard priority for the effective Redis
// TLS mode: an explicit policy-configured mode, then the Redis provider
// module's own mode, then redisTLSAuto. A provider module that requires TLS
// rejects any explicitly policy-configured mode other than redisTLSRequired,
// so a deployment cannot silently disable TLS against a provider that
// mandates it. It is a pure function extracted from hostPolicy.resolve so
// this priority and rejection logic is testable without a full policy
// document or environment lookup.
func resolveRedisTLSMode(policyMode, providerMode redisTLSMode, providerModule string) (redisTLSMode, redisTLSModeSource, error) {
	if policyMode == "" {
		if providerMode == "" {
			return redisTLSAuto, redisTLSModeSourceDefault, nil
		}
		return providerMode, redisTLSModeSourceProvider, nil
	}
	if providerMode == redisTLSRequired && policyMode != redisTLSRequired {
		return "", "", fmt.Errorf("redis provider module %q requires TLS", providerModule)
	}
	return policyMode, redisTLSModeSourcePolicy, nil
}

type resolvedHostPolicy struct {
	Profile                HostProfile
	SingleReplicaConfirmed bool
	OperationalBackend     string
	OperationalNamespace   string
	AllowVolatile          bool
	OperationalPostgresURL string
	RedisURL               string
	DatabaseNamespace      string
	RedisOptions           redisx.Options
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
	Redis         *redisPolicy    `json:"redis"`
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

type redisPolicy struct {
	Module                  string           `json:"module"`
	URLEnv                  string           `json:"url-env"`
	HostEnv                 string           `json:"host-env"`
	PortEnv                 string           `json:"port-env"`
	UsernameEnv             string           `json:"username-env"`
	PasswordEnv             string           `json:"password-env"`
	Session                 HostRedisSession `json:"session"`
	IsolateProcessNamespace *bool            `json:"isolate-process-namespace"`
	SingleReplica           *bool            `json:"single-replica"`
	SupportsCollection      *bool            `json:"supports-collection"`
	AllowPrivatePlaintext   bool             `json:"allow-private-plaintext"`
	TLS                     redisTLSPolicy   `json:"tls"`
}

type redisTLSPolicy struct {
	Mode             redisTLSMode `json:"mode"`
	ServerNameEnv    string       `json:"server-name-env"`
	CACertificateEnv string       `json:"ca-certificate-env"`
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
		if nestedRedis, exists := fields["redis"]; exists && string(nestedRedis) == "null" {
			return nil, errors.New("operational-store.redis must be an object")
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
	backend := "postgres"
	redis := redisPolicy{Module: "generic"}
	if policy.Operational == nil {
		policy.Operational = &operationalPolicy{Backend: "postgres"}
	}
	if policy.Operational != nil {
		backend = policy.Operational.Backend
		switch backend {
		case "redis":
			if policy.Operational.Postgres != nil {
				return nil, errors.New("redis operational-store cannot configure PostgreSQL")
			}
			if policy.Operational.SingleProcess || policy.Operational.AllowVolatile {
				return nil, errors.New("redis operational-store does not accept memory topology or volatile-state options")
			}
			if policy.Operational.Redis != nil {
				redis = *policy.Operational.Redis
			}
		case "memory":
			if policy.Operational.Redis != nil || policy.Operational.Postgres != nil {
				return nil, errors.New("memory operational-store cannot configure Redis or PostgreSQL")
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
			if policy.Operational.Redis != nil || policy.Operational.SingleProcess || policy.Operational.AllowVolatile {
				return nil, errors.New("postgres operational-store cannot configure Redis or memory topology")
			}
			profile.SupportsCollection = true
		default:
			return nil, fmt.Errorf("unsupported operational-store backend %q", backend)
		}
	}
	namespace, err := namespaces.Environment(lookup)
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		namespace = "hosted-dashboard"
	}
	operationalNamespace := namespace
	if policy.Operational != nil {
		if value := envValue(lookup, firstNonempty(policy.Operational.NamespaceEnv, "CAO_OPERATIONAL_NAMESPACE")); value != "" {
			operationalNamespace = value
		}
	}
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
	provider, err := resolveRedisProviderModule(redis)
	if err != nil {
		return nil, err
	}
	profile.RedisSession = provider.session
	profile.IsolateProcessNamespace = provider.isolateProcessNamespace
	profile.SingleReplica = provider.singleReplica
	profile.SupportsCollection = provider.supportsCollection
	profile.RequiresRedis = true
	if profile.SingleReplica && !supportsSingleReplica {
		return nil, fmt.Errorf(
			"host target module %q cannot guarantee the Redis provider's single-replica requirement",
			policy.Target.Module,
		)
	}
	if profile.SingleReplica && policy.Target.Replicas != 1 {
		return nil, fmt.Errorf("host target module %q requires replicas to be 1 for the Redis provider", policy.Target.Module)
	}
	if err := profile.validate(); err != nil {
		return nil, err
	}
	if err := validateRedisProfile(profile); err != nil {
		return nil, err
	}
	urlEnv := firstNonempty(redis.URLEnv, provider.urlEnv)
	hostEnv := firstNonempty(redis.HostEnv, provider.hostEnv)
	portEnv := firstNonempty(redis.PortEnv, provider.portEnv)
	usernameEnv := firstNonempty(redis.UsernameEnv, provider.usernameEnv)
	passwordEnv := firstNonempty(redis.PasswordEnv, provider.passwordEnv)
	tlsMode, tlsSource, err := resolveRedisTLSMode(redis.TLS.Mode, provider.tlsMode, redis.Module)
	if err != nil {
		return nil, err
	}
	if tlsMode != redisTLSAuto && tlsMode != redisTLSRequired && tlsMode != redisTLSDisabled {
		return nil, fmt.Errorf("unsupported Redis TLS mode %q", tlsMode)
	}
	serverLog.Printf("resolved redis tls mode=%s source=%s", tlsMode, tlsSource)
	redisURL, err := redisURLFromEnvironment(
		lookup, urlEnv, hostEnv, portEnv, usernameEnv, passwordEnv, tlsMode,
	)
	if err != nil {
		return nil, err
	}
	if redis.Module == "upstash" && !isUpstashRedisURL(redisURL) {
		return nil, errors.New("upstash Redis provider module requires an upstash.io endpoint")
	}
	return &resolvedHostPolicy{
		Profile:                profile,
		SingleReplicaConfirmed: !profile.SingleReplica || policy.Target.Replicas == 1,
		OperationalBackend:     backend,
		OperationalNamespace:   operationalNamespace,
		RedisURL:               redisURL,
		DatabaseNamespace:      namespace,
		CORS:                   cors,
		RedisOptions: redisx.Options{
			AllowPrivatePlaintext: redis.AllowPrivatePlaintext,
			SingleSession:         provider.session == HostRedisSerialized,
			ForceTLS:              tlsMode == redisTLSRequired,
			DisableTLS:            tlsMode == redisTLSDisabled,
			TLSServerName:         envValue(lookup, redis.TLS.ServerNameEnv),
			TLSCACertificatePEM:   envValue(lookup, redis.TLS.CACertificateEnv),
		},
	}, nil
}

func redisURLFromEnvironment(
	lookup func(string) (string, bool),
	urlEnv, hostEnv, portEnv, usernameEnv, passwordEnv string,
	tlsMode redisTLSMode,
) (string, error) {
	if rawURL := envValue(lookup, urlEnv); rawURL != "" {
		return rawURL, nil
	}
	host := envValue(lookup, hostEnv)
	if host == "" {
		return "", fmt.Errorf("redis connection requires environment variable %s", firstNonempty(urlEnv, hostEnv))
	}
	port := envValue(lookup, portEnv)
	if port == "" {
		port = "6379"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("redis port environment variable must contain a port number")
	}
	scheme := "redis"
	if tlsMode == redisTLSRequired {
		scheme = "rediss"
	}
	parsed := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	username := envValue(lookup, usernameEnv)
	password := envValue(lookup, passwordEnv)
	if username != "" || password != "" {
		parsed.User = url.UserPassword(username, password)
	}
	return parsed.String(), nil
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
