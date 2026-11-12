package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type nativePolicyRule func(any, string) error

var (
	nativeExactVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	nativeOwner        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	nativeSecret       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	nativeLogin        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
)

// Decode tokens rather than unmarshalling maps so duplicate authority keys never
// silently turn a malformed document into a different valid policy.
func nativeDecodeJSON(content []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value func() (any, error)
	value = func() (any, error) {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch token {
		case json.Delim('{'):
			result := map[string]any{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok {
					return nil, nativeErrorf("JSON mapping key must be a string")
				}
				if _, exists := result[name]; exists {
					return nil, fmt.Errorf("duplicate mapping key: %s", name)
				}
				result[name], err = value()
				if err != nil {
					return nil, err
				}
			}
			_, err := decoder.Token()
			return result, err
		case json.Delim('['):
			result := []any{}
			for decoder.More() {
				child, err := value()
				if err != nil {
					return nil, err
				}
				result = append(result, child)
			}
			_, err := decoder.Token()
			return result, err
		default:
			if _, ok := token.(json.Delim); ok {
				return nil, errors.New("unexpected JSON delimiter")
			}
			return token, nil
		}
	}
	result, err := value()
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected one JSON document")
	}
	return result, nil
}

func nativeReadValidatedPolicy() (map[string]any, error) {
	if _, err := repositoryPath(policyPath); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(policyPath)
	if err != nil {
		return nil, err
	}
	value, err := nativeDecodeJSON(content)
	if err != nil {
		return nil, fmt.Errorf("%s contains invalid JSON: %w", policyPath, err)
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("policy root must be a mapping")
	}
	return document, nativeValidatePolicy(document)
}

func nativePolicyObject(fields map[string]nativePolicyRule, required ...string) nativePolicyRule {
	return func(value any, at string) error {
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a mapping", at)
		}
		for _, key := range required {
			if _, exists := object[key]; !exists {
				return fmt.Errorf("%s.%s is required", at, key)
			}
		}
		for key, child := range object {
			rule, exists := fields[key]
			if !exists {
				return fmt.Errorf("%s contains unknown key: %s", at, key)
			}
			if err := rule(child, at+"."+key); err != nil {
				return err
			}
		}
		return nil
	}
}

func nativePolicyString(pattern *regexp.Regexp) nativePolicyRule {
	return func(value any, at string) error {
		text, ok := value.(string)
		if !ok || (pattern != nil && !pattern.MatchString(text)) {
			return fmt.Errorf("%s must be a valid string", at)
		}
		return nil
	}
}

func nativeEnum(values ...string) nativePolicyRule {
	return func(value any, at string) error {
		for _, allowed := range values {
			if value == allowed {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s", at, strings.Join(values, ", "))
	}
}

func nativePolicyBool(value any, at string) error {
	if _, ok := value.(bool); !ok {
		return fmt.Errorf("%s must be a boolean", at)
	}
	return nil
}

func nativeNumber(value any) float64 {
	switch value := value.(type) {
	case json.Number:
		number, _ := value.Float64()
		return number
	case int:
		return float64(value)
	case float64:
		return value
	default:
		return math.NaN()
	}
}

func nativeInteger(minimum, maximum float64) nativePolicyRule {
	return func(value any, at string) error {
		number := nativeNumber(value)
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < minimum || number > maximum {
			return fmt.Errorf("%s must be an integer between %g and %g", at, minimum, maximum)
		}
		return nil
	}
}

func nativePolicyStrings(pattern *regexp.Regexp, minimum, maximum int) nativePolicyRule {
	return func(value any, at string) error {
		values, ok := value.([]any)
		if !ok || len(values) < minimum || len(values) > maximum {
			return fmt.Errorf("%s must be a sequence with %d to %d entries", at, minimum, maximum)
		}
		seen := map[string]bool{}
		for _, value := range values {
			if err := nativePolicyString(pattern)(value, at); err != nil {
				return err
			}
			key := strings.ToLower(value.(string))
			if seen[key] {
				return fmt.Errorf("%s contains a duplicate value: %s", at, key)
			}
			seen[key] = true
		}
		return nil
	}
}

func nativeMapping(keys *regexp.Regexp, rule nativePolicyRule) nativePolicyRule {
	return func(value any, at string) error {
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a mapping", at)
		}
		seen := map[string]bool{}
		for key, child := range object {
			if !keys.MatchString(key) || seen[strings.ToLower(key)] {
				return fmt.Errorf("%s contains an invalid or duplicate identity: %s", at, key)
			}
			seen[strings.ToLower(key)] = true
			if err := rule(child, at+"."+key); err != nil {
				return err
			}
		}
		return nil
	}
}

func nativeValidatePolicy(document map[string]any) error {
	if document == nil {
		return errors.New("policy root must be a mapping")
	}
	// Public callers also build policies with []string and integer Go values.
	// Normalize those representations without changing the caller's document.
	content, err := json.Marshal(document)
	if err != nil {
		return err
	}
	normalized, err := nativeDecodeJSON(content)
	if err != nil {
		return err
	}
	document = normalized.(map[string]any)
	mode := nativeEnum("review", "live")
	defaults := map[string]nativePolicyRule{
		"mode": mode, "max-repositories": nativeInteger(1, 1000),
		"rollout-percent": nativeInteger(1, 100), "monthly-ai-credit-budget": nativeInteger(0, math.MaxFloat64),
	}
	worker := nativePolicyObject(map[string]nativePolicyRule{
		"workflow": nativePolicyString(slugPattern), "enabled": nativePolicyBool, "max-mode": mode,
	}, "workflow")
	campaign := map[string]nativePolicyRule{
		"workflow-values": nativeWorkflowValuesRule,
		"enabled":         nativePolicyBool,
		"icon":            nativeEnum("mark-github", "code", "repo", "server", "issue", "pull-request", "play", "eye", "shield", "meter", "graph", "codescan", "dependabot", "key", "beaker", "rocket", "workflow", "gear", "check-circle", "campaign", "external-link"),
		"targets":         nativeMapping(repositoryPattern, nativePolicyObject(map[string]nativePolicyRule{"mode": mode}, "mode")),
		"workers":         nativeMapping(slugPattern, worker),
	}
	for key, rule := range defaults {
		campaign[key] = rule
	}
	standalone := map[string]nativePolicyRule{
		"enabled": nativePolicyBool, "max-mode": mode,
		"targets": nativeMapping(repositoryPattern, nativePolicyObject(map[string]nativePolicyRule{"mode": mode}, "mode")),
	}
	for key, rule := range defaults {
		standalone[key] = rule
	}
	control := nativePolicyObject(map[string]nativePolicyRule{
		"scope": nativePolicyObject(map[string]nativePolicyRule{
			"allowed-owners":       nativePolicyStrings(nativeOwner, 0, math.MaxInt),
			"allowed-repositories": nativePolicyStrings(repositoryPattern, 0, math.MaxInt),
		}),
		"activity": nativePolicyObject(map[string]nativePolicyRule{
			"repositories": nativePolicyStrings(repositoryPattern, 1, math.MaxInt), "public-only": nativePolicyBool,
		}, "repositories"),
		"inventory": nativePolicyObject(map[string]nativePolicyRule{
			"max-scan-repositories": nativeInteger(1, 100000), "cell-count": nativeInteger(1, 1000),
			"cell-index": nativeInteger(0, math.MaxFloat64), "batch-size": nativeInteger(1, 100000), "batch-index": nativeInteger(0, math.MaxFloat64),
		}),
		"web":      nativePolicyObject(map[string]nativePolicyRule{"experimental": nativePolicyBool, "favicon": nativeValidateFavicon, "host": nativeValidateHost}),
		"defaults": nativePolicyObject(defaults), "campaigns": nativeMapping(slugPattern, nativePolicyObject(campaign)),
		"workflows": nativeMapping(slugPattern, nativePolicyObject(standalone)),
		"operations": nativeMapping(slugPattern, nativePolicyObject(map[string]nativePolicyRule{
			"enabled": nativePolicyBool, "mode": mode, "max-mode": mode,
			"max-repositories": nativeInteger(1, 1000),
			"targets":          nativeMapping(repositoryPattern, nativePolicyObject(map[string]nativePolicyRule{"mode": mode}, "mode")),
		})),
		"publishing": nativePolicyObject(map[string]nativePolicyRule{
			"enabled": nativePolicyBool, "control-repositories": nativePolicyStrings(repositoryPattern, 0, math.MaxInt),
			"reviewers": nativePolicyStrings(nativeLogin, 0, math.MaxInt),
		}),
		"marketplace": nativeValidateMarketplace,
	})
	rule := nativePolicyObject(map[string]nativePolicyRule{
		"$schema": nativeEnum(schemaURL), "version": nativeInteger(1, 1),
		"gh-aw-version": nativePolicyString(nativeExactVersion), "control-plane": control,
		"target-authority": nativePolicyObject(map[string]nativePolicyRule{
			"campaigns": nativeMapping(slugPattern, nativePolicyObject(map[string]nativePolicyRule{"authority": nativePolicyString(repositoryPattern)}, "authority")),
		}, "campaigns"),
	}, "version")
	if err := nativeRejectExpressions(document); err != nil {
		return err
	}
	if err := rule(document, "policy"); err != nil {
		return err
	}
	cp, exists := document["control-plane"]
	if !exists {
		if _, exists := document["target-authority"]; !exists {
			return errors.New("policy requires control-plane or target-authority")
		}
		return nil
	}
	if _, exists := document["gh-aw-version"]; !exists {
		return errors.New("gh-aw-version is required for a control plane")
	}
	c := cp.(map[string]any)
	scope, _ := c["scope"].(map[string]any)
	withinScope := func(repository string, emptyMeansAll bool) error {
		for key, identity := range map[string]string{"allowed-repositories": repository, "allowed-owners": strings.SplitN(repository, "/", 2)[0]} {
			if values, exists := scope[key]; exists {
				list := values.([]any)
				if len(list) == 0 && emptyMeansAll {
					continue
				}
				found := false
				for _, value := range list {
					found = found || strings.EqualFold(value.(string), identity)
				}
				if !found {
					return fmt.Errorf("repository %s is outside control-plane.scope.%s", repository, key)
				}
			}
		}
		return nil
	}
	if activity, ok := c["activity"].(map[string]any); ok {
		for _, value := range activity["repositories"].([]any) {
			if err := withinScope(value.(string), true); err != nil {
				return err
			}
		}
	}
	campaigns, _ := c["campaigns"].(map[string]any)
	workflowBindings := map[string]bool{}
	for name, value := range campaigns {
		workflowBindings[name] = true
		if name != "activity" {
			if _, exists := value.(map[string]any)["workflow-values"]; exists {
				return errors.New("workflow-values is only supported for the activity campaign")
			}
		}
		policy := value.(map[string]any)
		targets, _ := policy["targets"].(map[string]any)
		for repository := range targets {
			if err := withinScope(repository, false); err != nil {
				return err
			}
		}
		workflows := map[string]bool{}
		workers, _ := policy["workers"].(map[string]any)
		for _, value := range workers {
			workflow := value.(map[string]any)["workflow"].(string)
			if workflows[workflow] {
				return fmt.Errorf("campaign %s workers must declare unique workflow identities", name)
			}
			workflows[workflow] = true
			workflowBindings[workflow] = true
		}
	}
	standaloneWorkflows, _ := c["workflows"].(map[string]any)
	for name, value := range standaloneWorkflows {
		if workflowBindings[name] {
			return fmt.Errorf("standalone workflow identity %s conflicts with an existing declaration", name)
		}
		workflowBindings[name] = true
		targets, _ := value.(map[string]any)["targets"].(map[string]any)
		for repository := range targets {
			if err := withinScope(repository, false); err != nil {
				return err
			}
		}
	}
	operations, _ := c["operations"].(map[string]any)
	for name, value := range operations {
		if workflowBindings[name] {
			return fmt.Errorf("operation workflow identity %s conflicts with an existing declaration", name)
		}
		targets, _ := value.(map[string]any)["targets"].(map[string]any)
		for repository := range targets {
			if err := withinScope(repository, false); err != nil {
				return err
			}
		}
	}
	if inventory, ok := c["inventory"].(map[string]any); ok {
		count, index := 1.0, 0.0
		if value, exists := inventory["cell-count"]; exists {
			count = nativeNumber(value)
		}
		if value, exists := inventory["cell-index"]; exists {
			index = nativeNumber(value)
		}
		if index >= count {
			return errors.New("control-plane.inventory.cell-index must be smaller than cell-count")
		}
	}
	if publishing, ok := c["publishing"].(map[string]any); ok && publishing["enabled"] == true {
		if reviewers, _ := publishing["reviewers"].([]any); len(reviewers) == 0 {
			return errors.New("control-plane.publishing.reviewers is required when publishing is enabled")
		}
	}
	return nil
}

func nativeRejectExpressions(value any) error {
	switch value := value.(type) {
	case map[string]any:
		for _, child := range value {
			if err := nativeRejectExpressions(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := nativeRejectExpressions(child); err != nil {
				return err
			}
		}
	case string:
		if strings.Contains(value, "${{") {
			return errors.New("policy must not contain a GitHub Actions expression")
		}
	}
	return nil
}

func nativeValidateFavicon(value any, at string) error {
	text, ok := value.(string)
	if !ok || len(text) > 2048 || strings.ContainsAny(text, " \t\r\n?#") {
		return fmt.Errorf("%s must be a safe relative or HTTPS URL", at)
	}
	if strings.HasPrefix(text, "./") && len(text) > 2 && regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`).MatchString(text[2:]) {
		for _, part := range strings.Split(text, "/") {
			if part == ".." {
				return fmt.Errorf("%s must not traverse directories", at)
			}
		}
		return nil
	}
	u, err := url.Parse(text)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("%s must be a safe relative or HTTPS URL", at)
	}
	return nil
}

func nativeValidateMarketplace(value any, at string) error {
	registry := nativePolicyObject(map[string]nativePolicyRule{
		"id": nativePolicyString(slugPattern), "name": nativePolicyString(regexp.MustCompile(`(?s)^.{1,100}$`)),
		"repository": nativePolicyString(repositoryPattern), "ref": nativePolicyString(regexp.MustCompile(`(?s)^.{1,255}$`)),
		"format": nativeEnum("packages", "workflows"), "path": nativePolicyString(regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)),
		"api-url": nativePolicyString(regexp.MustCompile(`^https://\S+$`)),
		"auth": func(value any, at string) error {
			auth, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must be a mapping", at)
			}
			fields := map[string]nativePolicyRule{"type": nativeEnum("none", "pat", "github-app")}
			required := []string{"type"}
			switch auth["type"] {
			case "pat":
				fields["secret"] = nativePolicyString(nativeSecret)
				required = append(required, "secret")
			case "github-app":
				for _, key := range []string{"app-id-secret", "private-key-secret", "installation-id-secret"} {
					fields[key] = nativePolicyString(nativeSecret)
				}
				required = append(required, "app-id-secret", "private-key-secret", "installation-id-secret")
			}
			return nativePolicyObject(fields, required...)(value, at)
		},
	}, "id", "repository", "ref")
	rule := nativePolicyObject(map[string]nativePolicyRule{
		"cache-ttl-seconds": nativeInteger(30, 86400),
		"registries": func(value any, at string) error {
			registries, ok := value.([]any)
			if !ok || len(registries) > 32 {
				return fmt.Errorf("%s must be a sequence with at most 32 entries", at)
			}
			seen := map[string]bool{}
			for _, value := range registries {
				if err := registry(value, at); err != nil {
					return err
				}
				r := value.(map[string]any)
				id := r["id"].(string)
				if seen[id] {
					return fmt.Errorf("%s contains duplicate id: %s", at, id)
				}
				seen[id] = true
				if p, ok := r["path"].(string); ok && (strings.HasPrefix(p, "/") || path.Clean(p) == ".." || strings.Contains("/"+p+"/", "/../")) {
					return fmt.Errorf("%s contains an unsafe registry path", at)
				}
				if text, ok := r["api-url"].(string); ok {
					u, err := url.Parse(text)
					if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
						return fmt.Errorf("%s.api-url must be a credential-free HTTPS URL", at)
					}
				}
			}
			return nil
		},
	}, "registries")
	return rule(value, at)
}

func nativeValidateHost(value any, at string) error {
	env := nativePolicyString(nativeSecret)
	redis := nativePolicyObject(map[string]nativePolicyRule{
		"module":  nativeEnum("generic", "local", "upstash", "aws-elasticache", "redis-cloud", "gcp-memorystore", "railway", "render", "digitalocean"),
		"url-env": env, "host-env": env, "port-env": env, "username-env": env, "password-env": env, "namespace-env": env,
		"session": nativeEnum("pooled", "serialized"), "isolate-process-namespace": nativePolicyBool,
		"single-replica": nativePolicyBool, "supports-collection": nativePolicyBool, "allow-private-plaintext": nativePolicyBool,
		"tls": nativePolicyObject(map[string]nativePolicyRule{"mode": nativeEnum("auto", "required", "disabled"), "server-name-env": env, "ca-certificate-env": env}),
	}, "module")
	target := nativePolicyObject(map[string]nativePolicyRule{
		"module": nativeEnum("generic", "container", "azure-functions"), "name": nativePolicyString(slugPattern),
		"authentication": nativeEnum("github-oauth"), "listener": nativeEnum("process", "platform", "external"),
		"require-https": func(v any, p string) error {
			if v != true {
				return fmt.Errorf("%s must be true", p)
			}
			return nil
		},
		"trust-platform-proxy": nativePolicyBool, "supports-single-replica": nativePolicyBool, "replicas": nativeInteger(1, math.MaxFloat64),
	}, "module")
	rule := nativePolicyObject(map[string]nativePolicyRule{
		"target": target, "redis": redis,
		"operational-store": nativePolicyObject(map[string]nativePolicyRule{
			"backend": nativeEnum("redis", "memory", "postgres"), "single-process": nativePolicyBool, "allow-volatile": nativePolicyBool,
			"namespace-env": env, "redis": redis, "postgres": nativePolicyObject(map[string]nativePolicyRule{"url-env": env}),
		}, "backend"),
		"cors": nativePolicyObject(map[string]nativePolicyRule{
			"max-age": nativeInteger(1, 86400), "allowed-origins": func(value any, at string) error {
				origin := regexp.MustCompile(`^(?:https://(?:[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*|\[[0-9A-Fa-f:.]+\])|http://(?:localhost|127(?:\.[0-9]{1,3}){3}|\[::1\]))(?::[0-9]{1,5})?/?$`)
				if err := nativePolicyStrings(origin, 0, 32)(value, at); err != nil {
					return err
				}
				for _, value := range value.([]any) {
					u, err := url.Parse(value.(string))
					if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
						return fmt.Errorf("%s contains an invalid origin", at)
					}
					ip := net.ParseIP(u.Hostname())
					local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
					if u.Scheme != "https" && (u.Scheme != "http" || !local) {
						return fmt.Errorf("%s requires HTTPS or loopback HTTP origins", at)
					}
					if u.Port() != "" {
						port, err := strconv.Atoi(u.Port())
						if err != nil || port < 1 || port > 65535 {
							return fmt.Errorf("%s has an invalid port", at)
						}
					}
				}
				return nil
			},
		}),
	}, "target")
	if err := rule(value, at); err != nil {
		return err
	}
	host := value.(map[string]any)
	t := host["target"].(map[string]any)
	if t["module"] == "generic" {
		if t["authentication"] != "github-oauth" || t["listener"] == nil {
			return fmt.Errorf("%s.target generic module requires authentication and listener", at)
		}
		if t["trust-platform-proxy"] == true && t["listener"] != "platform" {
			return fmt.Errorf("%s.target.trust-platform-proxy requires a platform listener", at)
		}
	} else {
		for _, key := range []string{"authentication", "listener", "require-https", "trust-platform-proxy", "supports-single-replica"} {
			if _, exists := t[key]; exists {
				return fmt.Errorf("%s.target.%s is fixed by module %s", at, key, t["module"])
			}
		}
	}
	single := t["module"] == "container" || (t["module"] == "generic" && t["supports-single-replica"] == true)
	r, _ := host["redis"].(map[string]any)
	if operational, ok := host["operational-store"].(map[string]any); ok {
		if _, exists := host["redis"]; exists {
			return fmt.Errorf("%s.redis and operational-store cannot both select an operational backend", at)
		}
		backend := operational["backend"]
		if _, exists := operational["postgres"]; exists && backend != "postgres" {
			return fmt.Errorf("%s.operational-store.postgres requires the postgres backend", at)
		}
		if backend == "memory" {
			if operational["redis"] != nil || operational["single-process"] != true || operational["allow-volatile"] != true || !single || nativeNumber(t["replicas"]) != 1 || (t["module"] == "generic" && t["listener"] != "process") {
				return fmt.Errorf("%s memory requires acknowledged single-process, single-replica process-listener topology", at)
			}
			return nil
		}
		if operational["single-process"] == true || operational["allow-volatile"] == true {
			return fmt.Errorf("%s durable backends reject memory topology options", at)
		}
		if backend == "postgres" {
			if operational["redis"] != nil {
				return fmt.Errorf("%s postgres cannot configure Redis", at)
			}
			return nil
		}
		r, _ = operational["redis"].(map[string]any)
	}
	if r == nil {
		return nil
	}
	if r["module"] != "generic" {
		for _, key := range []string{"session", "isolate-process-namespace", "single-replica", "supports-collection"} {
			if _, exists := r[key]; exists {
				return fmt.Errorf("%s.redis.%s is fixed by module %s", at, key, r["module"])
			}
		}
	} else if (r["single-replica"] == true || r["isolate-process-namespace"] == true) && r["session"] != "serialized" {
		return fmt.Errorf("%s Redis single-replica and process isolation require a serialized session", at)
	}
	if (r["module"] == "upstash" || (r["module"] == "generic" && r["single-replica"] == true)) && (!single || nativeNumber(t["replicas"]) != 1) {
		return fmt.Errorf("%s target cannot guarantee the Redis module's single-replica requirement", at)
	}
	return nil
}
