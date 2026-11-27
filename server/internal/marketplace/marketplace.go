// Package marketplace resolves the read-only campaign package catalog
// described in specs/marketplace.md for the hosted Go dashboard backend.
//
// It mirrors activity/marketplace.mjs so both backends return the same safe
// package DTO from the same ordered registries: registries are read from the
// CAO policy (control-plane.marketplace), each is resolved independently so
// one registry's failure never affects another, and normalized results are
// safe to cache and to serve to dashboard clients because they never carry
// secret values or access tokens.
package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
)

var resolveLog = logger.New("cao:marketplace:resolve")

// SourceName is the canonical dashboard source name the query engine and
// generation loader use to request the marketplace catalog.
const SourceName = "marketplace-packages"

// DefaultCacheTTL applies when the policy omits or sets an invalid
// cache-ttl-seconds value.
const DefaultCacheTTL = 5 * time.Minute

const (
	maxPolicyBytes         = 4 << 20 // 4 MiB: cao.json is small; this bounds a misconfigured or hostile policy file.
	maxManifestBytes       = 256 * 1024
	maxReadmeBytes         = 256 * 1024
	maxPackagesPerRegistry = 500
)

// internalPackages are catalog-owned directories that are never themselves
// installable packages.
var internalPackages = map[string]bool{"activity": true, "dashboard": true}

// AuthType enumerates the supported registry authentication modes.
type AuthType string

const (
	AuthNone      AuthType = "none"
	AuthPAT       AuthType = "pat"
	AuthGitHubApp AuthType = "github-app"
)

// Auth describes how a registry authenticates. Every field below other than
// Type is an environment variable *name*, never a secret value: resolvers
// read the named variable from the trusted hosted backend's own environment.
type Auth struct {
	Type                 AuthType `json:"type"`
	Secret               string   `json:"secret,omitempty"`
	AppIDSecret          string   `json:"app-id-secret,omitempty"`
	PrivateKeySecret     string   `json:"private-key-secret,omitempty"`
	InstallationIDSecret string   `json:"installation-id-secret,omitempty"`
}

// Registry is one ordered marketplace source. Earlier registries have higher
// precedence when two registries expose the same package coordinate.
type Registry struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Repository string `json:"repository"`
	Path       string `json:"path,omitempty"`
	Ref        string `json:"ref"`
	Format     string `json:"format,omitempty"`
	APIURL     string `json:"api-url,omitempty"`
	Auth       Auth   `json:"auth,omitempty"`
}

// Config is the parsed control-plane.marketplace policy section.
type Config struct {
	CacheTTL   time.Duration
	Registries []Registry
}

// Package is the normalized, safe package DTO shared with activity/marketplace.mjs.
type Package struct {
	ID                 string          `json:"id"`
	RegistryID         string          `json:"registry-id"`
	RegistryName       string          `json:"registry-name"`
	RegistryPrecedence int             `json:"registry-precedence"`
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	Publisher          string          `json:"publisher"`
	Repository         string          `json:"repository"`
	RepositoryLink     *RepositoryLink `json:"repository-link,omitempty"`
	Path               string          `json:"path"`
	Ref                string          `json:"ref"`
	ResolvedCommit     string          `json:"resolved-commit"`
	Version            string          `json:"version"`
	Icon               string          `json:"icon"`
	Artwork            string          `json:"artwork"`
	Contents           []string        `json:"contents"`
	Readme             string          `json:"readme"`
	ReadmePath         string          `json:"readme-path"`
	Source             string          `json:"source"`
	AddCommand         string          `json:"add-command"`
	Stars              *int            `json:"stars"`
	Forks              *int            `json:"forks"`
}

// RepositoryLink points only to a recognized public GitHub repository web URL.
type RepositoryLink struct {
	Relation string `json:"relation"`
	Href     string `json:"href"`
	Label    string `json:"label"`
}

// Row converts the package to a dashboard row. Field names are kebab-case to
// match the canonical data model used by every other source.
func (p Package) Row() model.Row {
	contents := p.Contents
	if contents == nil {
		contents = []string{}
	}
	row := model.Row{
		"id":                  p.ID,
		"registry-id":         p.RegistryID,
		"registry-name":       p.RegistryName,
		"registry-precedence": p.RegistryPrecedence,
		"package-name":        p.Name,
		"package-description": p.Description,
		"publisher":           p.Publisher,
		"repository":          p.Repository,
		"path":                p.Path,
		"package-ref":         p.Ref,
		"resolved-commit":     p.ResolvedCommit,
		"package-version":     p.Version,
		"package-icon":        p.Icon,
		"package-artwork":     p.Artwork,
		"package-contents":    contents,
		"package-readme":      p.Readme,
		"package-readme-path": p.ReadmePath,
		"package-source":      p.Source,
		"add-command":         p.AddCommand,
		"stars":               p.Stars,
		"forks":               p.Forks,
	}
	if p.RepositoryLink != nil {
		row["repository-link"] = model.Row{
			"relation": p.RepositoryLink.Relation,
			"href":     p.RepositoryLink.Href,
			"label":    p.RepositoryLink.Label,
		}
	}
	return row
}

// RegistryDiagnostic reports one registry's resolution outcome without any
// credential-derived detail.
type RegistryDiagnostic struct {
	RegistryID string `json:"registry-id"`
	Status     string `json:"status"`
	Packages   int    `json:"packages,omitempty"`
	Message    string `json:"message,omitempty"`
}

// Row converts the diagnostic to a dashboard-safe row.
func (d RegistryDiagnostic) Row() model.Row {
	row := model.Row{"registry-id": d.RegistryID, "status": d.Status}
	if d.Status == "available" {
		row["packages"] = d.Packages
	}
	if d.Message != "" {
		row["message"] = d.Message
	}
	return row
}

// Result is the outcome of resolving every configured registry.
type Result struct {
	Packages    []Package
	Diagnostics []RegistryDiagnostic
}

type policyDocument struct {
	ControlPlane struct {
		Marketplace *marketplaceSection `json:"marketplace"`
	} `json:"control-plane"`
}

type marketplaceSection struct {
	CacheTTLSeconds int        `json:"cache-ttl-seconds"`
	Registries      []Registry `json:"registries"`
}

// ParseConfig reads the control-plane.marketplace section out of a full CAO
// policy document. A policy without a marketplace section resolves to zero
// registries rather than an error, matching the catalog's opt-in schema.
func ParseConfig(data []byte) (*Config, error) {
	if len(data) > maxPolicyBytes {
		return nil, fmt.Errorf("CAO policy exceeds %d bytes", maxPolicyBytes)
	}
	var document policyDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse CAO policy: %w", err)
	}
	if document.ControlPlane.Marketplace == nil {
		return &Config{Registries: []Registry{}}, nil
	}
	section := document.ControlPlane.Marketplace
	ttl := time.Duration(section.CacheTTLSeconds) * time.Second
	registries := section.Registries
	if registries == nil {
		registries = []Registry{}
	}
	return &Config{CacheTTL: ttl, Registries: registries}, nil
}

// LoadConfigFile reads and parses the CAO policy at path.
func LoadConfigFile(path string) (*Config, error) {
	// #nosec G304 -- path is an operator-controlled policy location (a flag/env
	// default), not user input.
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read CAO policy %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	// Read one byte past the limit so an oversized file is rejected without
	// materializing it; ParseConfig enforces the limit on the bytes read.
	data, err := io.ReadAll(io.LimitReader(file, maxPolicyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read CAO policy %q: %w", path, err)
	}
	return ParseConfig(data)
}

var (
	registryIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)
	secretNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	safePathCharset   = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	commitPattern     = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)
)

// isSafePath rejects absolute paths, disallowed characters, and any ".."
// path segment. Go's RE2 engine has no lookahead, so this is checked in code
// instead of by a single regular expression as activity/marketplace.mjs does.
func isSafePath(value string) bool {
	if strings.HasPrefix(value, "/") {
		return false
	}
	if !safePathCharset.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

// ValidateRegistry checks structural and authentication-reference safety and
// returns a normalized copy (trimmed path, defaulted name). It never reads
// secrets; it only checks that auth fields *look like* environment variable
// names.
func ValidateRegistry(registry Registry, index int) (Registry, error) {
	if !registryIDPattern.MatchString(registry.ID) {
		return Registry{}, fmt.Errorf("registry %d id is invalid", index)
	}
	if !repositoryPattern.MatchString(registry.Repository) {
		return Registry{}, fmt.Errorf("registry %s repository is invalid", registry.ID)
	}
	if strings.TrimSpace(registry.Ref) == "" {
		return Registry{}, fmt.Errorf("registry %s ref is required", registry.ID)
	}
	if registry.Format != "" && registry.Format != "packages" && registry.Format != "workflows" {
		return Registry{}, fmt.Errorf("registry %s format must be packages or workflows", registry.ID)
	}
	path := strings.Trim(registry.Path, "/")
	if strings.HasPrefix(registry.Path, "/") || !isSafePath(path) {
		return Registry{}, fmt.Errorf("registry %s path is invalid", registry.ID)
	}
	if registry.APIURL != "" && !strings.HasPrefix(registry.APIURL, "https://") {
		return Registry{}, fmt.Errorf("registry %s api-url must use https", registry.ID)
	}
	if err := validateAuth(registry.Auth); err != nil {
		return Registry{}, fmt.Errorf("registry %s auth: %w", registry.ID, err)
	}
	normalized := registry
	normalized.Path = path
	if normalized.Name == "" {
		normalized.Name = registry.ID
	}
	return normalized, nil
}

func validateAuth(auth Auth) error {
	switch auth.Type {
	case "", AuthNone:
		return nil
	case AuthPAT:
		if !secretNamePattern.MatchString(auth.Secret) {
			return fmt.Errorf("pat secret must reference an environment variable name")
		}
		return nil
	case AuthGitHubApp:
		for _, value := range []string{auth.AppIDSecret, auth.PrivateKeySecret, auth.InstallationIDSecret} {
			if !secretNamePattern.MatchString(value) {
				return fmt.Errorf("github-app secrets must reference environment variable names")
			}
		}
		return nil
	default:
		return fmt.Errorf("authentication type %q is unsupported", auth.Type)
	}
}

// apiBase returns the registry's GitHub REST API base URL with any trailing
// slash removed, defaulting to github.com.
func apiBase(registry Registry) string {
	base := registry.APIURL
	if base == "" {
		base = "https://api.github.com"
	}
	return strings.TrimRight(base, "/")
}

func fallbackRegistryID(registry Registry, index int) string {
	if registry.ID != "" {
		return registry.ID
	}
	return fmt.Sprintf("registry-%d", index)
}

// safeDiagnosticMessage renders err for a registry diagnostic with any secret
// *value* referenced by the registry's auth configuration redacted first,
// mirroring safeDiagnostic in activity/marketplace.mjs. This matters even
// though resolvers never intentionally return secrets: an underlying HTTP
// client or upstream API can otherwise echo a token/PAT/private-key back into
// an error message (e.g. via a URL or transport error), and diagnostics are
// exposed to dashboard clients.
func safeDiagnosticMessage(err error, registry Registry, opts Options) string {
	message := err.Error()
	env := opts.env()
	for _, name := range authSecretNames(registry.Auth) {
		if name == "" {
			continue
		}
		if value, ok := env(name); ok && value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	if len(message) > 500 {
		return message[:500]
	}
	return message
}

func authSecretNames(auth Auth) []string {
	return []string{auth.Secret, auth.AppIDSecret, auth.PrivateKeySecret, auth.InstallationIDSecret}
}

// registryOutcome is one registry's resolution outcome within a Resolve pass:
// its packages (empty on failure), its diagnostic, and whether the result was
// served from cache rather than resolved against the GitHub API. Splitting
// this into its own unit lets the three distinct outcomes below — an invalid
// registry, a cache hit, and a fresh resolution — be exercised directly
// without assembling a whole Resolve call each time.
type registryOutcome struct {
	packages   []Package
	diagnostic RegistryDiagnostic
	cacheHit   bool
}

// resolveOneRegistry validates, then resolves or serves-from-cache, exactly
// one configured registry. It never returns an error: every failure mode is
// captured in the returned diagnostic so one bad registry cannot abort the
// rest of a Resolve pass.
func resolveOneRegistry(
	ctx context.Context, raw Registry, index int, generation string, ttl time.Duration, cache Cache, opts Options,
) registryOutcome {
	registry, err := ValidateRegistry(raw, index)
	if err != nil {
		return registryOutcome{diagnostic: RegistryDiagnostic{
			RegistryID: fallbackRegistryID(raw, index), Status: "unavailable", Message: safeDiagnosticMessage(err, raw, opts),
		}}
	}
	if cached, hit := loadCachedRegistry(ctx, cache, registry.ID, generation); hit {
		for i := range cached {
			cached[i].RepositoryLink = repositoryLink(cached[i].Repository, apiBase(registry))
		}
		return registryOutcome{
			packages:   cached,
			diagnostic: RegistryDiagnostic{RegistryID: registry.ID, Status: "available", Packages: len(cached)},
			cacheHit:   true,
		}
	}
	resolved, err := ResolveRegistry(ctx, registry, index, opts)
	if err != nil {
		return registryOutcome{diagnostic: RegistryDiagnostic{
			RegistryID: registry.ID, Status: "unavailable", Message: safeDiagnosticMessage(err, registry, opts),
		}}
	}
	storeCachedRegistry(ctx, cache, registry.ID, generation, ttl, resolved)
	return registryOutcome{
		packages:   resolved,
		diagnostic: RegistryDiagnostic{RegistryID: registry.ID, Status: "available", Packages: len(resolved)},
	}
}

// Resolve resolves every configured registry in order, isolating failures
// into diagnostics, and returns the deduplicated, precedence-sorted package
// catalog. generation isolates the Cache by dashboard data revision so a
// cached result from one revision is never served under another. cache may be
// nil to disable caching.
func Resolve(ctx context.Context, config *Config, generation string, cache Cache, opts Options) *Result {
	if config == nil {
		return &Result{Packages: []Package{}, Diagnostics: []RegistryDiagnostic{}}
	}
	ttl := config.CacheTTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	var packages []Package
	diagnostics := make([]RegistryDiagnostic, 0, len(config.Registries))
	cacheHits, unavailable := 0, 0
	for index, raw := range config.Registries {
		outcome := resolveOneRegistry(ctx, raw, index, generation, ttl, cache, opts)
		packages = append(packages, outcome.packages...)
		diagnostics = append(diagnostics, outcome.diagnostic)
		if outcome.cacheHit {
			cacheHits++
		}
		if outcome.diagnostic.Status == "unavailable" {
			unavailable++
		}
	}
	// One summary per resolution pass is a meaningful boundary worth
	// observing: it is never called from a polling or per-package loop.
	resolveLog.Printf("resolved registries total=%d cache_hits=%d unavailable=%d",
		len(config.Registries), cacheHits, unavailable)
	return &Result{Packages: sortAndDedupe(packages), Diagnostics: diagnostics}
}

func sortAndDedupe(packages []Package) []Package {
	sort.SliceStable(packages, func(i, j int) bool {
		left, right := packages[i], packages[j]
		if left.RegistryPrecedence != right.RegistryPrecedence {
			return left.RegistryPrecedence < right.RegistryPrecedence
		}
		if left.Repository != right.Repository {
			return left.Repository < right.Repository
		}
		return left.Path < right.Path
	})
	seen := map[string]bool{}
	ordered := make([]Package, 0, len(packages))
	for _, entry := range packages {
		coordinate := strings.ToLower(entry.Repository + "/" + entry.Path)
		if seen[coordinate] {
			continue
		}
		seen[coordinate] = true
		ordered = append(ordered, entry)
	}
	return ordered
}
