package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	resolutionEngine = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	resolutionRole   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 ._:@/-]{0,127}$`)
	resolutionModel  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/?=&+:@-]{0,199}$`)
	resolutionCause  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	reflectionModel  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/+-]{0,199}$`)
	piStartupModel   = regexp.MustCompile(`^\[gh-aw/pi-models-json\] fatal: Pi model "([a-zA-Z0-9][a-zA-Z0-9._/?=+-]{0,199})" is not advertised by the configured ([a-zA-Z0-9_-]{1,64}) proxy endpoint;`)
	copilotProvider  = regexp.MustCompile(`^copilot-(?:responses|completions)(?:-[0-9]+)?$`)
)

type modelFailure struct {
	engine, role, model, provider, cause string
}

type modelAuditSession struct {
	failures []modelFailure
	active   bool
	exit     *int
}

// Model causality follows github/gh-aw@v1.0.1 audit_model_resolution.go and
// audit_model_availability.go. Raw stdio and reflection remain ephemeral;
// only validated identifiers and fixed finding/recommendation text survive.
func modelAuditFindings(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]any, []any, []string, error) {
	limits = limits.defaults()
	gaps := []string{}
	read := func(name string, maxBytes int) ([]byte, error) {
		source := map[string]string{"aw_session.jsonl": "session", "agent-stdio.log": "stdio", "awf-reflect.json": "reflection"}[name]
		names := candidates(files, name)
		if len(names) == 0 {
			return nil, nil
		}
		if len(names) != 1 {
			gaps = append(gaps, "ambiguous-model-audit-"+source)
			return nil, nil
		}
		content := files[names[0]]
		if len(content) > min(limits.Bytes, maxBytes) {
			return nil, ErrCapacity
		}
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			gaps = append(gaps, "invalid-model-audit-"+source)
			return nil, nil
		}
		return content, nil
	}
	raw, err := read("aw_session.jsonl", limits.Bytes)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(candidates(files, "aw_session.jsonl")) > 1 || (len(candidates(files, "aw_session.jsonl")) == 1 && raw == nil) {
		return nil, nil, gaps, nil
	}
	session := modelAuditSession{}
	if raw != nil {
		session, err = parseModelAuditSession(ctx, raw, identity, limits)
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, nil, nil, err
			}
			// Do not reinterpret a malformed session as absent model evidence.
			return nil, nil, append(gaps, "invalid-model-resolution-audit"), nil
		}
	}
	log, err := read("agent-stdio.log", 32<<20)
	if err != nil {
		return nil, nil, nil, err
	}
	lines := []string{}
	if log != nil {
		if err := scanEvidence(ctx, log, limits, func(_ int, line []byte) error {
			lines = append(lines, string(line))
			return nil
		}); err != nil {
			return nil, nil, nil, err
		}
	}
	legacy := false
	if len(session.failures) == 0 && fields["conclusion"] == "failure" {
		for _, line := range lines {
			match := piStartupModel.FindStringSubmatch(line)
			if len(match) == 3 {
				session.failures = []modelFailure{{engine: "pi", role: "main", model: match[1], provider: match[2], cause: "model_unavailable"}}
				exit := -1
				session.exit = &exit
				session.active = false
				legacy = true
				break
			}
		}
	}
	if len(session.failures) == 0 {
		return nil, nil, gaps, ctx.Err()
	}
	reflection, err := read("awf-reflect.json", 8<<20)
	if err != nil {
		return nil, nil, nil, err
	}
	var inventory map[string]any
	if reflection != nil {
		var shape struct {
			Complete  bool `json:"models_fetch_complete"`
			Endpoints []struct {
				Provider   string            `json:"provider"`
				Configured bool              `json:"configured"`
				Models     []json.RawMessage `json:"models"`
				Routes     []struct {
					Model     string   `json:"model_id"`
					Endpoints []string `json:"supported_endpoints"`
				} `json:"routing_models"`
			} `json:"endpoints"`
		}
		if decode(reflection, &inventory) != nil || inventory == nil || !matches(inventory, identity) ||
			decode(reflection, &shape) != nil {
			gaps = append(gaps, "invalid-model-audit-reflection")
			inventory = nil
		} else if err := boundModelInventory(ctx, inventory, limits); err != nil {
			return nil, nil, nil, err
		}
	}
	// Each causal class is emitted once, like upstream's grouping of roles.
	order := []string{"model_unavailable", "model_endpoint_unsupported", "model_availability_unknown", "model_resolution_failed"}
	grouped := map[string][]modelFailure{}
	work, inventoryWork := 0, len(array(inventory["endpoints"]))
	for _, endpoint := range array(inventory["endpoints"]) {
		inventoryWork += len(array(object(endpoint)["models"]))
	}
	absence := map[[2]string]bool{}
	for _, failure := range session.failures {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		code := "model_resolution_failed"
		stopped := false
		for _, line := range lines {
			work++
			if work > limits.Entries {
				return nil, nil, nil, ErrCapacity
			}
			if modelStopLine(line, failure) || (legacy && piStartupModel.MatchString(line)) {
				stopped = true
				break
			}
		}
		if fields["conclusion"] == "failure" && !session.active && session.exit != nil && *session.exit != 0 && stopped {
			confirmed := false
			if failure.cause == "model_unavailable" {
				key := [2]string{failure.provider, failure.model}
				var checked bool
				confirmed, checked = absence[key]
				if !checked {
					work += inventoryWork
					if work > limits.Entries {
						return nil, nil, nil, ErrCapacity
					}
					confirmed = inventoryExcludes(inventory, failure.provider, failure.model)
					absence[key] = confirmed
				}
			}
			switch {
			case failure.cause == "unsupported_endpoint":
				code = "model_endpoint_unsupported"
			case confirmed:
				code = "model_unavailable"
			default:
				code = "model_availability_unknown"
			}
		}
		grouped[code] = append(grouped[code], failure)
	}
	findings, recommendations := []any{}, []any{}
	for _, code := range order {
		failures := grouped[code]
		if len(failures) == 0 {
			continue
		}
		severity, title, action := "high", "", ""
		switch code {
		case "model_unavailable":
			title, action = "Model Unavailable", "Replace the unavailable model pin with a model advertised by the same provider, verify endpoint compatibility, and recompile the workflow"
		case "model_endpoint_unsupported":
			title, action = "Model Endpoint Unsupported", "Choose a model that supports the endpoint the engine calls, and recompile the workflow"
		case "model_availability_unknown":
			title, action = "Model Availability Unverified", "Inspect same-run model discovery, provider authentication, and reflection before changing model pins"
		case "model_resolution_failed":
			severity, title = "low", "Model Resolution Failure Recorded"
		}
		identifiers := make([]string, 0, len(failures))
		for _, failure := range failures {
			identifiers = append(identifiers, fmt.Sprintf("engine %s role %q configured model %q (provider %s, recorded cause %s)",
				failure.engine, failure.role, failure.model, failure.provider, failure.cause))
		}
		finding := map[string]any{"code": code, "category": "configuration", "severity": severity, "title": title,
			"description": strings.Join(identifiers, "; ")}
		findings = append(findings, finding)
		if action != "" {
			recommendations = append(recommendations, map[string]any{"priority": "high", "action": action})
		}
	}
	return findings, recommendations, gaps, nil
}

func parseModelAuditSession(ctx context.Context, content []byte, identity Identity, limits Limits) (modelAuditSession, error) {
	result := modelAuditSession{}
	header, execution := false, false
	seen := map[modelFailure]bool{}
	err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
		var event map[string]any
		if decode(raw, &event) != nil || event == nil || !matches(event, identity) || !matches(object(event["data"]), identity) {
			return ErrIdentity
		}
		data, provenance := object(event["data"]), object(event["provenance"])
		kind := text(event["type"])
		if !header {
			version, numeric := data["version"].(json.Number)
			if kind != "session.format" || provenance["component"] != "collector" || !numeric || version.String() != "1" {
				return errors.New("invalid model audit session header")
			}
			header = true
			return nil
		}
		switch {
		case kind == "model.resolution":
			if data["status"] != "failed" {
				return nil
			}
			if provenance["component"] != "model_resolution" || provenance["phase"] != "agent" ||
				provenance["path"] != "agent/model-resolution.json" {
				return errors.New("invalid model resolution provenance")
			}
			failure := modelFailure{text(data["engine"]), text(data["role"]), text(data["configured_model"]), text(data["provider"]), text(data["cause"])}
			resolved, present := data["resolved_model"]
			if !resolutionEngine.MatchString(failure.engine) || !resolutionRole.MatchString(failure.role) ||
				!resolutionModel.MatchString(failure.model) || !resolutionCause.MatchString(failure.cause) ||
				!present || resolved != nil || (data["provider"] != nil && !resolutionEngine.MatchString(failure.provider)) {
				return errors.New("invalid model resolution identifiers")
			}
			if !seen[failure] {
				result.failures = append(result.failures, failure)
				seen[failure] = true
			}
		case kind == "agent.execution" && provenance["component"] == "execution" && provenance["phase"] == "agent":
			if execution {
				return errors.New("ambiguous model audit execution")
			}
			execution = true
			var record struct {
				Data struct {
					Categories []string `json:"categories"`
					ExitCode   *int     `json:"exitCode"`
				} `json:"data"`
			}
			if decode(raw, &record) == nil {
				result.exit = record.Data.ExitCode
			}
		case provenance["component"] == "agent" && provenance["phase"] == "agent":
			if kind == "session.start" || kind == "session.init" || strings.HasPrefix(kind, "assistant.") || strings.HasPrefix(kind, "tool.") {
				result.active = true
			}
		}
		return nil
	})
	if err == nil && !header {
		err = errors.New("missing model audit session header")
	}
	return result, err
}

func modelStopLine(line string, failure modelFailure) bool {
	refusal := strings.Contains(strings.ToLower(line), "refusing to start") &&
		(strings.Contains(line, "sub-agent '"+failure.role+"'") || (failure.role == "main" && strings.Contains(line, "'"+failure.model+"'")))
	specific := fmt.Sprintf("model resolution: engine=%s role=%s configured=%s failed cause=%s:", failure.engine, failure.role, failure.model, failure.cause)
	return refusal || (strings.Contains(line, specific) && (failure.role == "main" || strings.Contains(line, "The requested model is not supported")))
}

func boundModelInventory(ctx context.Context, inventory map[string]any, limits Limits) error {
	count := len(array(inventory["endpoints"]))
	for _, endpoint := range array(inventory["endpoints"]) {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := object(endpoint)
		count += len(array(entry["models"])) + len(array(entry["routing_models"]))
		if count > limits.Entries {
			return ErrCapacity
		}
	}
	return nil
}

func inventoryExcludes(inventory map[string]any, provider, model string) bool {
	if inventory["models_fetch_complete"] != true || provider == "" {
		return false
	}
	var endpoint map[string]any
	for _, candidate := range array(inventory["endpoints"]) {
		entry := object(candidate)
		if entry["configured"] == true && normalizedModelProvider(text(entry["provider"])) == normalizedModelProvider(provider) {
			if endpoint != nil {
				return false
			}
			endpoint = entry
		}
	}
	models, present := endpoint["models"].([]any)
	if !present {
		return false
	}
	requested := strings.ToLower(model)
	if prefix, suffix, hasProvider := strings.Cut(requested, "/"); hasProvider {
		requested = strings.TrimSuffix(suffix, ":"+prefix)
	}
	requested, _, _ = strings.Cut(requested, "?")
	ids := make([]string, 0, len(models))
	for _, candidate := range models {
		id := text(candidate)
		if id == "" {
			entry := object(candidate)
			for _, name := range []string{"id", "name"} {
				if entry[name] != nil {
					if _, ok := entry[name].(string); !ok {
						return false
					}
				}
			}
			id = firstText(entry["id"], entry["name"])
		}
		if !reflectionModel.MatchString(id) {
			return false
		}
		ids = append(ids, strings.ToLower(id))
	}
	return !slices.Contains(ids, requested)
}

func normalizedModelProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if copilotProvider.MatchString(provider) {
		return "github-copilot"
	}
	switch provider {
	case "github", "copilot", "github-copilot":
		return "github-copilot"
	case "codex", "openai":
		return "openai"
	case "gemini", "google":
		return "google"
	default:
		return provider
	}
}
