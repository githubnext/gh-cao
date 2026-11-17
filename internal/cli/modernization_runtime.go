package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/dop251/goja"

	cao "github.com/githubnext/gh-cao"
)

var modernizationImports = regexp.MustCompile(`(?m)^import [^\n]+\n|^export \{[^\n]+\n`)
var modernizationExports = regexp.MustCompile(`(?m)^export (async function|function|class|const) `)

// The inputs are embedded, reviewed modules with static one-line imports.
// Each module retains its own lexical scope; only named exports are linked.
func modernizationModule(source, prelude, exports string) string {
	source = modernizationImports.ReplaceAllString(source, "")
	source = modernizationExports.ReplaceAllString(source, "$1 ")
	return "(function(){" + prelude + "\n" + source + "\nreturn {" + exports + "};})()"
}

func modernizationRuntime(ctx context.Context, request func(string, map[string]any) (map[string]any, error), run func(*goja.Runtime) (string, error)) (string, error) {
	vm := goja.New()
	if err := vm.Set("__sha256", func(text string) string {
		sum := sha256.Sum256([]byte(text))
		return hex.EncodeToString(sum[:])
	}); err != nil {
		return "", err
	}
	if err := vm.Set("__byteLength", func(text string) int { return len(text) }); err != nil {
		return "", err
	}
	if err := vm.Set("__request", func(route, parameters string) string {
		var values map[string]any
		if err := json.Unmarshal([]byte(parameters), &values); err != nil {
			panic(vm.NewGoError(err))
		}
		response, err := request(route, values)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		content, err := json.Marshal(response)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return string(content)
	}); err != nil {
		return "", err
	}
	prelude := `const Buffer={byteLength:__byteLength};
const process={env:{}}, console={log(){},error(){}};
function createHash(algorithm) {
 if(algorithm!=="sha256") throw new Error("Unsupported digest");
 let text=""; return {update(value){text+=value;return this;},
 digest(format){if(format!=="hex")throw new Error("Unsupported encoding");return __sha256(text);}};
}
const client={request(route,parameters={}){return JSON.parse(__request(route,JSON.stringify(parameters)));}};`
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-done:
		}
	}()
	if _, err := vm.RunString(prelude); err != nil {
		return "", err
	}
	for _, module := range []struct{ name, path, prelude, exports string }{
		{"authority", ".github/workflows/shared/authority.mjs", "", "AuthorityError,narrowLimit,resolveAuthority,validateTargetScope"},
		{"acquisition", ".github/workflows/shared/value-acquisition.mjs", "", "normalizeValueAcquisition"},
		{"policy", ".github/workflows/shared/policy.mjs", "const {AuthorityError:PolicyError,narrowLimit,resolveAuthority}=authority;const {normalizeValueAcquisition}=acquisition;", "parsePolicy,effectivePolicy"},
		{"engagement", "modernization/engagement.mjs", "", "validatePlan,planDigest,readyTasks,taskMarker,taskComplete,systemReadScope"},
		{"evidence", "modernization/github.mjs", "const {taskComplete,taskMarker}=engagement;", "collectEvidence"},
	} {
		source, err := cao.OperatorSources.ReadFile(module.path)
		if err != nil {
			return "", err
		}
		if _, err := vm.RunString("const " + module.name + "=" + modernizationModule(string(source), module.prelude, module.exports)); err != nil {
			return "", fmt.Errorf("initialize modernization %s: %w", module.name, err)
		}
	}
	return run(vm)
}

func modernizationEvaluate(ctx context.Context, input any, request func(string, map[string]any) (map[string]any, error), expression string, output any) error {
	content, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, err := modernizationRuntime(ctx, request, func(vm *goja.Runtime) (string, error) {
		if err := vm.Set("__input", string(content)); err != nil {
			return "", err
		}
		value, err := vm.RunString("(async function(){const input=JSON.parse(__input);return JSON.stringify(" + expression + ");})()")
		if err != nil {
			return "", err
		}
		promise, ok := value.Export().(*goja.Promise)
		if !ok || promise.State() == goja.PromiseStatePending {
			return "", fmt.Errorf("modernization runtime did not settle")
		}
		if promise.State() == goja.PromiseStateRejected {
			return "", fmt.Errorf("modernization: %s", promise.Result().String())
		}
		return promise.Result().String(), nil
	})
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(result), output)
}

func modernizationAPIRequest(ctx context.Context, gh ghExecutor, allowed []string, route string, parameters map[string]any) (map[string]any, error) {
	if !strings.HasPrefix(route, "GET /repos/") {
		return nil, fmt.Errorf("modernization evidence requires a repository GET")
	}
	endpoint := strings.TrimPrefix(route, "GET /")
	query := url.Values{}
	for name, value := range parameters {
		token := "{" + name + "}"
		if strings.Contains(endpoint, token) {
			endpoint = strings.ReplaceAll(endpoint, token, url.PathEscape(fmt.Sprint(value)))
		} else {
			query.Set(name, fmt.Sprint(value))
		}
	}
	parts := strings.Split(endpoint, "/")
	if len(parts) < 3 || !containsRepository(allowed, parts[1]+"/"+parts[2]) {
		return nil, fmt.Errorf("modernization evidence is outside the complete approved scope")
	}
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	stdout, _, err := gh(ctx, "api", "--hostname", "github.com", "--include", endpoint)
	if err != nil {
		return nil, fmt.Errorf("required modernization evidence is unavailable: %w", err)
	}
	if len(stdout) > 16<<20 {
		return nil, fmt.Errorf("modernization evidence page exceeds the native response bound")
	}
	headers, body, found := strings.Cut(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n\n")
	if !found {
		return nil, fmt.Errorf("modernization evidence response lacks HTTP headers")
	}
	var data any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return nil, err
	}
	link := ""
	for _, line := range strings.Split(headers, "\n") {
		name, value, _ := strings.Cut(line, ":")
		if strings.EqualFold(name, "Link") {
			link = strings.TrimSpace(value)
		}
	}
	return map[string]any{"data": data, "headers": map[string]any{"link": link}}, nil
}

func containsRepository(repositories []string, repository string) bool {
	for _, candidate := range repositories {
		if strings.EqualFold(candidate, repository) {
			return true
		}
	}
	return false
}
