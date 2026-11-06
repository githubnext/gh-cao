package collectnative

import (
	"context"
	_ "embed"
	"path"
	"regexp"
	"strings"
)

// Pinned gh-aw v1.0.1 model_identity.go and workflow/data/model_aliases.json.
//
//go:embed gh-aw-v1.0.1-model-aliases.json
var nativeModelAliases []byte

var nativeModelDate = regexp.MustCompile(`-[0-9]{4}(?:-[0-9]{2}-[0-9]{2}|[0-9]{4})$`)
var nativeClaudeVersion = regexp.MustCompile(`^(claude-.+)-([0-9]+)-([0-9]+)$`)
var nativeCopilotProvider = regexp.MustCompile(`^copilot-(?:responses|completions)(?:-[0-9]+)?$`)

type auditModelResolver struct {
	aliases map[string][]string
	ctx     context.Context
	work    int
	limit   int
}

func auditModelName(model string) string {
	provider, name, qualified := strings.Cut(model, "/")
	if qualified {
		return strings.TrimSuffix(name, ":"+provider)
	}
	return model
}

func auditModelIdentity(model string) string {
	model, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(model)), "?")
	return nativeClaudeVersion.ReplaceAllString(nativeModelDate.ReplaceAllString(auditModelName(model), ""), "$1-$2.$3")
}

func newAuditModelResolver(ctx context.Context, declarations []any, limits Limits) *auditModelResolver {
	resolver := &auditModelResolver{ctx: ctx, limit: limits.defaults().Entries}
	_ = decode(nativeModelAliases, &resolver.aliases)
	concrete := map[string]bool{}
	for _, value := range declarations {
		row := object(value)
		model, _, _ := strings.Cut(text(row["model"]), "?")
		normalized := auditModelIdentity(model)
		if normalized == "" || strings.ContainsAny(model, "*?[]{}$ ") || len(resolver.aliases[normalized]) > 0 {
			continue
		}
		for _, pattern := range array(row["patterns"]) {
			name := text(pattern)
			if !strings.ContainsAny(name, "*?[") && auditModelIdentity(name) == normalized {
				concrete[normalized] = true
			}
		}
	}
	for _, value := range declarations {
		row := object(value)
		model := text(row["model"])
		if model != "" && len(array(row["patterns"])) > 0 && !concrete[auditModelIdentity(model)] {
			patterns := []string{}
			for _, pattern := range array(row["patterns"]) {
				patterns = append(patterns, text(pattern))
			}
			resolver.aliases[auditModelIdentity(model)] = patterns
		}
	}
	return resolver
}

func (r *auditModelResolver) matches(pattern, observed, provider string) (bool, error) {
	visited := map[string]bool{}
	queue, patterns := []string{pattern}, []string{}
	for len(queue) > 0 {
		if err := r.ctx.Err(); err != nil {
			return false, err
		}
		r.work++
		if r.work > r.limit {
			return false, ErrCapacity
		}
		key := auditModelIdentity(queue[0])
		queue = queue[1:]
		if visited[key] {
			continue
		}
		visited[key] = true
		for _, value := range r.aliases[key] {
			r.work++
			if r.work > r.limit {
				return false, ErrCapacity
			}
			if !strings.ContainsAny(value, "/*?") && len(r.aliases[auditModelIdentity(value)]) > 0 {
				queue = append(queue, value)
			} else {
				patterns = append(patterns, value)
			}
		}
	}
	patterns = append(patterns, pattern)
	for _, candidate := range patterns {
		r.work++
		if r.work > r.limit {
			return false, ErrCapacity
		}
		if auditModelPattern(candidate, observed, provider) {
			return true, nil
		}
	}
	return false, nil
}

func auditModelPattern(pattern, observed, provider string) bool {
	pattern, _, _ = strings.Cut(strings.ToLower(pattern), "?")
	observed, _, _ = strings.Cut(strings.ToLower(observed), "?")
	original := observed
	patternProvider, model, qualified := strings.Cut(pattern, "/")
	observedProvider, _, observedQualified := strings.Cut(observed, "/")
	if qualified {
		if provider == "" && observedQualified {
			provider = observedProvider
		}
		if provider != "" && auditModelProvider(patternProvider) != auditModelProvider(provider) {
			return false
		}
		pattern = model
	}
	if observedQualified {
		if provider != "" && auditModelProvider(observedProvider) != auditModelProvider(provider) {
			return auditModelPattern(model, original, "")
		}
		observed = auditModelName(original)
	}
	for _, p := range []string{pattern, auditModelIdentity(pattern)} {
		for _, candidate := range []string{observed, auditModelIdentity(observed)} {
			if match, err := path.Match(p, candidate); err == nil && match {
				return true
			}
		}
	}
	return false
}

func auditModelProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if nativeCopilotProvider.MatchString(provider) {
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
