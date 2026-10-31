package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var hostedLog = logger.New("cao:server:hosted")

func NewHostedAppFromEnv(
	ctx context.Context,
	listen string,
	certFile string,
	keyFile string,
	siteDirectory string,
	dashboardQueriesPath string,
	databaseQueriesPath string,
	mcpEnabled bool,
	agentCatalogPath string,
	mcpContractPath string,
	logger *log.Logger,
	requiredBackend ...string,
) (*App, error) {
	if len(requiredBackend) > 1 {
		return nil, errors.New("only one operational backend requirement is supported")
	}
	requested := ""
	if len(requiredBackend) == 1 {
		requested = requiredBackend[0]
	}
	if requested != "" && requested != "redis" && requested != "memory" && requested != "postgres" {
		return nil, errors.New("--operational-store must be redis, memory, or postgres")
	}
	host, err := loadHostPolicyFromEnv()
	if err != nil {
		return nil, err
	}
	if host.Profile.Listener != HostListenerProcess {
		return nil, fmt.Errorf("host target module %q does not own a process listener", host.Profile.Name)
	}
	if requested != "" && host.OperationalBackend != requested {
		return nil, fmt.Errorf("--operational-store %s requires a matching reviewed control-plane.web.host policy (selected: %s)", requested, host.OperationalBackend)
	}
	return newHostedAppWithPolicy(ctx, host, listen, certFile, keyFile, siteDirectory, dashboardQueriesPath, databaseQueriesPath, logger, mcpEnabled, agentCatalogPath, mcpContractPath)
}

// NewExternallyHostedAppFromEnv builds an OAuth-protected CAO service without
// opening a listener. The host policy must explicitly delegate listener
// ownership; credentials and transport policy remain CAO-owned.
func NewExternallyHostedAppFromEnv(
	ctx context.Context,
	siteDirectory, dashboardQueriesPath, databaseQueriesPath string,
	logger *log.Logger,
) (*App, error) {
	host, err := loadHostPolicyFromEnv()
	if err != nil {
		return nil, err
	}
	if host.Profile.Listener != HostListenerExternal {
		return nil, fmt.Errorf("host target module %q does not delegate listener ownership to an external host", host.Profile.Name)
	}
	return newHostedAppWithPolicy(ctx, host, "", "", "", siteDirectory, dashboardQueriesPath, databaseQueriesPath, logger, false, "", "")
}

func newHostedAppWithPolicy(
	ctx context.Context,
	host *resolvedHostPolicy,
	listen, certFile, keyFile, siteDirectory, dashboardQueriesPath, databaseQueriesPath string,
	logger *log.Logger,
	mcpEnabled bool,
	agentCatalogPath, mcpContractPath string,
) (*App, error) {
	definitions, err := ParseDashboardQueries(dashboardQueriesPath)
	if err != nil {
		return nil, err
	}
	sourceDirectory := strings.TrimSpace(os.Getenv("CAO_SOURCE_DIRECTORY"))
	collector, err := CollectorConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if collector == nil && sourceDirectory == "" {
		return nil, errors.New("CAO_SOURCE_DIRECTORY is required")
	}
	if host.Profile.SingleProcess && collector != nil && collector.AdmitOnly {
		return nil, errors.New("memory operational-store requires co-resident collection, not admission-only mode")
	}
	store, revocationKeyPrefix, err := newOperationalStore(ctx, host)
	if err != nil {
		return nil, err
	}
	initialized := false
	defer func() {
		if !initialized {
			_ = store.Close()
		}
	}()
	webhookSecret := os.Getenv("CAO_GITHUB_WEBHOOK_SECRET")
	if len(webhookSecret) < 32 {
		return nil, errors.New("CAO_GITHUB_WEBHOOK_SECRET must contain at least 32 characters")
	}
	adminUsers := splitCSV(os.Getenv("CAO_GITHUB_ADMIN_USERS"))
	if len(adminUsers) == 0 {
		return nil, errors.New("CAO_GITHUB_ADMIN_USERS requires at least one GitHub login")
	}
	trustedProxyPrefixes, err := parseTrustedProxyPrefixes(os.Getenv("CAO_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return nil, err
	}
	trustForwarded, trustedProxyPrefixes := hostedProxyTrust(listen, certFile, trustedProxyPrefixes)
	postgresURL := strings.TrimSpace(os.Getenv("CAO_POSTGRES_URL"))
	if postgresURL == "" {
		return nil, errors.New("CAO_POSTGRES_URL is required")
	}
	databaseNamespace := strings.TrimSpace(host.RedisNamespace)
	if databaseNamespace == "" {
		databaseNamespace = "hosted-dashboard"
	}
	database, err := postgresx.OpenWithNamespace(ctx, postgresURL, databaseNamespace)
	if err != nil {
		return nil, errors.New("connect to dashboard Postgres failed")
	}
	auditLog, err := audit.FromEnv()
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	admissionConfig, err := admissionConfigFromEnv()
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	brokerConfig, err := tokenBrokerConfigFromEnv()
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	config := Config{
		Admission:              admissionConfig,
		TokenBroker:            brokerConfig,
		Database:               database,
		Audit:                  auditLog,
		HostProfile:            host.Profile,
		SingleReplicaConfirmed: host.SingleReplicaConfirmed,
		AllowVolatile:          host.AllowVolatile,
		Listen:                 listen,
		CertFile:               certFile,
		KeyFile:                keyFile,
		SiteDirectory:          siteDirectory,
		DashboardQueries:       definitions,
		AgentCatalogPath:       agentCatalogPath,
		MCPContractPath:        mcpContractPath,
		MCPEnabled:             mcpEnabled,
		ActionsRepository:      strings.TrimSpace(os.Getenv("CAO_MCP_ACTIONS_REPOSITORY")),
		DatabaseQueriesPath:    databaseQueriesPath,
		SourceDirectory:        sourceDirectory,
		Collector:              collector,
		WebhookSecret:          webhookSecret,
		AdminUsers:             adminUsers,
		CORS:                   host.CORS,
		Proxy: ProxyPolicy{
			AllowedHosts:         splitCSV(os.Getenv("CAO_ALLOWED_HOSTS")),
			RequireHTTPS:         host.Profile.RequiresHTTPS,
			TrustForwarded:       trustForwarded,
			TrustedProxyPrefixes: trustedProxyPrefixes,
		},
		GitHubOAuth: &GitHubOAuthConfig{
			ClientID:                    os.Getenv("CAO_GITHUB_CLIENT_ID"),
			ClientSecret:                os.Getenv("CAO_GITHUB_CLIENT_SECRET"),
			RedirectURL:                 os.Getenv("CAO_GITHUB_REDIRECT_URL"),
			SessionSecret:               os.Getenv("CAO_SESSION_SECRET"),
			PreviousSessionSecret:       os.Getenv("CAO_SESSION_SECRET_PREVIOUS"),
			AllowedUsers:                splitCSV(os.Getenv("CAO_GITHUB_ALLOWED_USERS")),
			AllowedOrganizations:        splitCSV(os.Getenv("CAO_GITHUB_ALLOWED_ORGS")),
			AllowedTeams:                splitCSV(os.Getenv("CAO_GITHUB_ALLOWED_TEAMS")),
			LocalDevelopmentUser:        os.Getenv("CAO_GITHUB_LOCAL_USER"),
			AdminReauthenticationMaxAge: envStrictDuration("CAO_ADMIN_REAUTH_MAX_AGE"),
			SSOOrganizations:            splitCSV(os.Getenv("CAO_GITHUB_SSO_ORGANIZATIONS")),
			RevocationKeyPrefix:         revocationKeyPrefix,
		},
		Logger: logger,
	}
	config.TokenBrokerSingleProcessConfirmed = os.Getenv("CAO_TOKEN_BROKER_SINGLE_PROCESS") == "true"
	app, err := New(ctx, store, config)
	if err != nil {
		_ = database.Close()
	} else {
		app.ownedDatabase = database
		app.ownedOperational = store
		initialized = true
	}
	return app, err
}

func hostedProxyTrust(listen, certFile string, configured []netip.Prefix) (bool, []netip.Prefix) {
	// Direct TLS uses the actual transport unless a proxy was explicitly configured.
	trustForwarded := len(configured) > 0 || (certFile == "" && isLoopbackListen(listen))
	prefixes := append([]netip.Prefix(nil), configured...)
	if trustForwarded {
		prefixes = append(prefixes, loopbackProxyPrefixes()...)
	}
	return trustForwarded, prefixes
}

// hostedRedisURLRejection classifies why validateHostedRedisURL rejected a
// hosted Redis URL. It is useful for diagnosing a misconfigured deployment
// without logging the URL itself, which can carry embedded credentials.
type hostedRedisURLRejection string

const (
	hostedRedisURLRejectionNone           hostedRedisURLRejection = "none"
	hostedRedisURLRejectionUnparsable     hostedRedisURLRejection = "unparsable"
	hostedRedisURLRejectionPlaintextOptIn hostedRedisURLRejection = "plaintext-not-opted-in"
	hostedRedisURLRejectionPublicHostname hostedRedisURLRejection = "public-hostname"
)

// classifyHostedRedisURL decides whether a hosted Redis URL is acceptable,
// given already-parsed scheme and hostname, without performing the URL
// parsing itself. It is a pure function extracted from validateHostedRedisURL
// so the TLS-required, opt-in, and private-hostname decisions are testable
// directly against scheme and hostname values, without constructing a
// *url.URL for every case.
func classifyHostedRedisURL(scheme, hostname string, allowPrivatePlaintext, forceTLS bool) hostedRedisURLRejection {
	if strings.EqualFold(scheme, "rediss") || forceTLS {
		return hostedRedisURLRejectionNone
	}
	if !strings.EqualFold(scheme, "redis") || !allowPrivatePlaintext {
		return hostedRedisURLRejectionPlaintextOptIn
	}
	if !privateRedisHostname(hostname) {
		return hostedRedisURLRejectionPublicHostname
	}
	return hostedRedisURLRejectionNone
}

func validateHostedRedisURL(redisURL string, allowPrivatePlaintext, forceTLS bool) error {
	parsed, err := url.Parse(strings.TrimSpace(redisURL))
	if err != nil {
		hostedLog.Printf("hosted redis url rejected reason=%s", hostedRedisURLRejectionUnparsable)
		return errors.New("invalid hosted Redis URL")
	}
	switch classifyHostedRedisURL(parsed.Scheme, parsed.Hostname(), allowPrivatePlaintext, forceTLS) {
	case hostedRedisURLRejectionPlaintextOptIn:
		hostedLog.Printf("hosted redis url rejected reason=%s", hostedRedisURLRejectionPlaintextOptIn)
		return errors.New("hosted mode requires rediss:// Redis transport unless private plaintext Redis is explicitly enabled")
	case hostedRedisURLRejectionPublicHostname:
		hostedLog.Printf("hosted redis url rejected reason=%s", hostedRedisURLRejectionPublicHostname)
		return errors.New("hosted plaintext Redis requires a private IP or single-label service hostname")
	default:
		return nil
	}
}

func isUpstashRedisURL(redisURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(redisURL))
	if err != nil {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	return hostname == "upstash.io" || strings.HasSuffix(hostname, ".upstash.io")
}

func privateRedisHostname(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	if hostname == "" {
		return false
	}
	if strings.HasSuffix(hostname, ".railway.internal") {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return ip.IsPrivate() || ip.IsLoopback()
	}
	return !strings.Contains(hostname, ".")
}

func parseTrustedProxyPrefixes(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, item := range splitCSV(value) {
		prefix, err := netip.ParsePrefix(item)
		if err != nil || !privateProxyPrefix(prefix) {
			return nil, errors.New("CAO_TRUSTED_PROXY_CIDRS must contain only private network CIDR prefixes")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func privateProxyPrefix(prefix netip.Prefix) bool {
	for _, private := range []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("::1/128"),
	} {
		if prefix.Bits() >= private.Bits() && private.Contains(prefix.Addr().Unmap()) {
			return true
		}
	}
	return false
}

func loopbackProxyPrefixes() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
}

// StoreFromEnv opens a shared operational backend for standalone collection
// roles. Process-local stores cannot share admission authority across roles.
func StoreFromEnv(ctx context.Context) (operational.Store, error) {
	host, err := loadHostPolicyFromEnv()
	if err != nil {
		return nil, err
	}
	if err := validateStandaloneOperational(host); err != nil {
		return nil, err
	}
	store, _, err := newOperationalStore(ctx, host)
	return store, err
}
