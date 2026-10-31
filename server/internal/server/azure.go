package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var proxyLog = logger.New("cao:server:proxy")

type ProxyPolicy struct {
	AllowedHosts         []string
	RequireHTTPS         bool
	TrustForwarded       bool
	TrustedProxyPrefixes []netip.Prefix
}

func validateHostedMode(store operational.Store, config *Config) error {
	profile := config.HostProfile
	if profile.Authentication != HostAuthenticationOAuth {
		if profile.Listener == HostListenerProcess {
			if err := ValidateListen(config.Listen, config.CertFile, config.KeyFile); err != nil {
				return err
			}
			if profile.RequiresHTTPS && (config.CertFile == "" || config.KeyFile == "") {
				return fmt.Errorf("host profile %q requires HTTPS", profile.Name)
			}
		}
		return nil
	}
	if strings.TrimSpace(config.AccessToken) != "" {
		return errors.New("hosted mode does not support local bearer capabilities")
	}
	if store == nil {
		return errors.New("hosted mode requires operational storage")
	}
	if len(config.Proxy.AllowedHosts) == 0 {
		return errors.New("hosted mode requires an explicit trusted proxy host policy")
	}
	policy := config.Proxy
	if profile.RequiresHTTPS && !policy.RequireHTTPS {
		return errors.New("hosted mode requires HTTPS")
	}
	if profile.Listener == HostListenerExternal && config.Proxy.TrustForwarded &&
		len(config.Proxy.TrustedProxyPrefixes) == 0 {
		return errors.New("externally hosted forwarded headers require explicit trusted proxy CIDRs")
	}
	if profile.Listener == HostListenerProcess {
		if config.CertFile == "" && isLoopbackListen(config.Listen) && !config.Proxy.TrustForwarded {
			config.Proxy.TrustForwarded = true
			config.Proxy.TrustedProxyPrefixes = loopbackProxyPrefixes()
		}
		if config.Proxy.TrustForwarded && len(config.Proxy.TrustedProxyPrefixes) == 0 {
			return errors.New("hosted forwarded headers require explicit trusted proxy CIDRs")
		}
		if err := validateHostedListen(
			config.Listen, config.CertFile, config.KeyFile, config.Proxy.TrustForwarded,
		); err != nil {
			return err
		}
	}
	if config.GitHubOAuth == nil {
		return errors.New("hosted mode requires GitHub OAuth configuration")
	}
	if err := config.GitHubOAuth.validate(); err != nil {
		return err
	}
	if err := validateLocalDevelopmentOAuth(*config); err != nil {
		return err
	}
	return nil
}

// proxyRejectionReason names why classifyProxyRequest rejected a request. It
// is stable across host-value and header-content changes, so it is useful to
// log without exposing the request's Host, X-Forwarded-* values, or client
// address.
type proxyRejectionReason string

const (
	proxyRejectionNone            proxyRejectionReason = ""
	proxyRejectionUntrustedPeer   proxyRejectionReason = "untrusted-proxy-peer"
	proxyRejectionEmptyHost       proxyRejectionReason = "empty-host"
	proxyRejectionHostNotAllowed  proxyRejectionReason = "host-not-allowed"
	proxyRejectionInsecureRequest proxyRejectionReason = "insecure-request"
)

// classifyProxyRequest decides whether request satisfies policy and, when it
// does not, which precondition failed. It is a pure function extracted from
// validProxyRequest so the trusted-peer check, host allow-listing, and the
// HTTPS requirement are each independently testable against a plain
// *http.Request, and so a rejection can be logged by reason without
// exposing the request's host or forwarded-header values.
func classifyProxyRequest(request *http.Request, policy ProxyPolicy) proxyRejectionReason {
	host := request.Host
	secure := request.TLS != nil
	if policy.TrustForwarded {
		if len(policy.TrustedProxyPrefixes) > 0 && !trustedProxyPeer(request.RemoteAddr, policy.TrustedProxyPrefixes) {
			return proxyRejectionUntrustedPeer
		}
		if forwarded := forwardedHeader(request, "X-Forwarded-Host"); forwarded != "" {
			host = forwarded
		}
		proto := strings.ToLower(strings.TrimSpace(strings.Split(forwardedHeader(request, "X-Forwarded-Proto"), ",")[0]))
		secure = proto == "https"
	}
	host = strings.ToLower(strings.TrimSpace(strings.Split(host, ",")[0]))
	if host == "" {
		return proxyRejectionEmptyHost
	}
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	allowed := false
	for _, candidate := range policy.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(candidate), host) {
			allowed = true
			break
		}
	}
	if !allowed {
		return proxyRejectionHostNotAllowed
	}
	if policy.RequireHTTPS && !secure {
		return proxyRejectionInsecureRequest
	}
	return proxyRejectionNone
}

// validProxyRequest reports whether request satisfies policy, logging the
// specific precondition that failed on rejection so a misconfigured proxy
// policy or an unexpected caller is diagnosable without exposing the
// request's host or forwarded-header values.
func validProxyRequest(request *http.Request, policy ProxyPolicy) bool {
	reason := classifyProxyRequest(request, policy)
	if reason != proxyRejectionNone {
		proxyLog.Printf("proxy request rejected reason=%s", reason)
		return false
	}
	return true
}

func trustedProxyPeer(remoteAddress string, prefixes []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	address = address.Unmap()
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func forwardedHeader(request *http.Request, name string) string {
	values := request.Header.Values(name)
	for valueIndex := len(values) - 1; valueIndex >= 0; valueIndex-- {
		parts := strings.Split(values[valueIndex], ",")
		for partIndex := len(parts) - 1; partIndex >= 0; partIndex-- {
			if value := strings.TrimSpace(parts[partIndex]); value != "" {
				return value
			}
		}
	}
	return ""
}

// processTelemetry lazily installs OpenTelemetry providers exactly once for
// the life of the process. The Azure Functions runtime reuses the process
// across invocations, so the exporters it configures must outlive any
// single request; bundling the sync.Once and its cached error into one type
// keeps that pairing in a single place instead of spreading a package-level
// mutex and error variable across the file.
type processTelemetry struct {
	once     sync.Once
	err      error
	shutdown telemetry.Shutdown
}

// ensure configures telemetry on the first call and returns the cached
// outcome on every call thereafter. A configuration failure is logged at
// most once, on the call that performed the setup, and is treated as
// non-fatal: callers keep serving requests without exported telemetry rather
// than failing the whole handler over a bad exporter configuration.
func (p *processTelemetry) ensure(logger *log.Logger) error {
	p.once.Do(func() {
		p.shutdown, p.err = configureProcessTelemetry()
		if p.err != nil && logger != nil {
			logger.Printf("telemetry configuration failed, continuing without exported telemetry")
		}
	})
	return p.err
}

// ShutdownAzureFunctionsTelemetry flushes process-owned exporters after the
// custom handler has finished serving requests.
func ShutdownAzureFunctionsTelemetry(ctx context.Context) error {
	return azureProcessTelemetry.close(ctx)
}

func (p *processTelemetry) close(ctx context.Context) error {
	if p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}

// configureProcessTelemetry installs the providers with a process-
// lifetime context rather than a request-scoped one, since the resulting
// exporter is shared by every future invocation handled by this process.
func configureProcessTelemetry() (telemetry.Shutdown, error) {
	version := strings.TrimSpace(os.Getenv("CAO_BUILD_VERSION"))
	if version == "" {
		version = "unknown"
	}
	return telemetry.Setup(context.Background(), version)
}

var azureProcessTelemetry processTelemetry

func NewAzureFunctionsHandlerFromEnv(ctx context.Context, siteDirectory, dashboardQueriesPath string, logger *log.Logger) (http.Handler, error) {
	//nolint:contextcheck // configureProcessTelemetry intentionally uses context.Background(): the exporter it
	// configures must outlive the single request/invocation that happens to trigger processTelemetry.ensure.
	_ = azureProcessTelemetry.ensure(logger)
	localSimulation, err := azureLocalSimulationFromEnv()
	if err != nil {
		return nil, err
	}
	host, err := loadHostPolicyFromEnv()
	if err != nil {
		return nil, err
	}
	allowedHosts := splitCSV(os.Getenv("CAO_AZURE_ALLOWED_HOSTS"))
	redirectURL := os.Getenv("CAO_GITHUB_REDIRECT_URL")
	if err := validateAzureLocalEndpoints(localSimulation, allowedHosts, redirectURL); err != nil {
		return nil, err
	}
	if host.Profile.Listener != HostListenerPlatform {
		return nil, fmt.Errorf("host target module %q does not delegate listener ownership", host.Profile.Name)
	}
	if host.OperationalBackend == "redis" {
		if err := validateAzureRedisURL(host.RedisURL, localSimulation); err != nil {
			return nil, err
		}
	}
	store, revocationPrefix, err := newOperationalStore(ctx, host)
	if err != nil {
		return nil, err
	}
	initialized := false
	defer func() {
		if !initialized {
			_ = store.Close()
		}
	}()
	profile := azureLocalSimulationProfile(host.Profile, localSimulation)
	definitions, err := ParseDashboardQueries(dashboardQueriesPath)
	if err != nil {
		return nil, err
	}
	// The collection profile is optional. When it is unconfigured this reads
	// as nil and the Functions front end behaves exactly as before.
	collector, err := CollectorConfigFromEnv()
	if err != nil {
		return nil, err
	}
	postgresURL := strings.TrimSpace(os.Getenv("CAO_POSTGRES_URL"))
	if postgresURL == "" {
		return nil, errors.New("azure Functions mode requires CAO_POSTGRES_URL")
	}
	databaseQueries := strings.TrimSpace(os.Getenv("CAO_DATABASE_QUERIES"))
	if databaseQueries == "" {
		return nil, errors.New("azure Functions mode requires CAO_DATABASE_QUERIES")
	}
	databaseNamespace := strings.TrimSpace(host.RedisNamespace)
	if databaseNamespace == "" {
		databaseNamespace = "hosted-dashboard"
	}
	database, err := postgresx.OpenWithNamespace(ctx, postgresURL, databaseNamespace)
	if err != nil {
		return nil, fmt.Errorf("connect to dashboard Postgres: %w", err)
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
	app, err := New(ctx, store, Config{
		Admission:              admissionConfig,
		TokenBroker:            brokerConfig,
		Database:               database,
		Audit:                  auditLog,
		DatabaseQueriesPath:    databaseQueries,
		HostProfile:            profile,
		SingleReplicaConfirmed: host.SingleReplicaConfirmed,
		SiteDirectory:          siteDirectory,
		DashboardQueries:       definitions,
		Collector:              collector,
		WebhookSecret:          os.Getenv("CAO_GITHUB_WEBHOOK_SECRET"),
		AdminUsers:             splitCSV(os.Getenv("CAO_GITHUB_ADMIN_USERS")),
		CORS:                   host.CORS,
		Proxy: ProxyPolicy{
			AllowedHosts:   allowedHosts,
			RequireHTTPS:   !localSimulation,
			TrustForwarded: true,
		},
		GitHubOAuth: &GitHubOAuthConfig{
			ClientID:                    os.Getenv("CAO_GITHUB_CLIENT_ID"),
			ClientSecret:                os.Getenv("CAO_GITHUB_CLIENT_SECRET"),
			RedirectURL:                 redirectURL,
			SessionSecret:               os.Getenv("CAO_SESSION_SECRET"),
			PreviousSessionSecret:       os.Getenv("CAO_SESSION_SECRET_PREVIOUS"),
			AllowedUsers:                splitCSV(os.Getenv("CAO_GITHUB_ALLOWED_USERS")),
			AllowedOrganizations:        splitCSV(os.Getenv("CAO_GITHUB_ALLOWED_ORGS")),
			AllowedTeams:                splitCSV(os.Getenv("CAO_GITHUB_ALLOWED_TEAMS")),
			AdminReauthenticationMaxAge: envStrictDuration("CAO_ADMIN_REAUTH_MAX_AGE"),
			SSOOrganizations:            splitCSV(os.Getenv("CAO_GITHUB_SSO_ORGANIZATIONS")),
			RevocationKeyPrefix:         revocationPrefix,
		},
		Logger: logger,
	})
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := app.initializeRedisMaintenance(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	maintenanceCtx, stopMaintenance := context.WithCancel(context.WithoutCancel(ctx))
	app.startTask(func() { app.runRedisMaintenance(maintenanceCtx, redisMaintenanceInterval) })
	app.startTask(func() { app.oauth.runRevocationWorker(maintenanceCtx) })
	app.ownedDatabase = database
	app.ownedOperational = store
	initialized = true
	return &azureFunctionsHandler{Handler: app.AzureFunctionsHandler(), app: app, stopMaintenance: stopMaintenance}, nil
}

type azureFunctionsHandler struct {
	http.Handler
	app             *App
	stopMaintenance context.CancelFunc
}

func (h *azureFunctionsHandler) Drain() {
	h.app.Drain()
	if h.stopMaintenance != nil {
		h.stopMaintenance()
	}
}

func azureLocalSimulationFromEnv() (bool, error) {
	switch value := strings.TrimSpace(os.Getenv("CAO_AZURE_LOCAL_SIMULATION")); value {
	case "":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, errors.New("CAO_AZURE_LOCAL_SIMULATION must be 1 when enabled")
	}
}

func validateAzureRedisURL(redisURL string, localSimulation bool) error {
	parsed, err := url.Parse(redisURL)
	if err != nil {
		return errors.New("azure Functions mode requires a valid Redis URL")
	}
	if strings.EqualFold(parsed.Scheme, "rediss") {
		return nil
	}
	if !localSimulation || !strings.EqualFold(parsed.Scheme, "redis") {
		return errors.New("azure Functions mode requires rediss:// Redis transport")
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	address := net.ParseIP(host)
	if address == nil || !address.IsLoopback() {
		return errors.New("local Azure simulation requires loopback Redis")
	}
	return nil
}

func validateAzureLocalEndpoints(localSimulation bool, allowedHosts []string, redirectURL string) error {
	if !localSimulation {
		return nil
	}
	for _, host := range allowedHosts {
		if !isLoopbackHost(host) {
			return errors.New("local Azure simulation requires loopback allowed hosts")
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(redirectURL))
	if err != nil || !isLoopbackHost(parsed.Hostname()) {
		return errors.New("local Azure simulation requires a loopback OAuth redirect URL")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (a *App) AzureFunctionsHandler() http.Handler {
	return a.Handler()
}

func splitCSV(value string) []string {
	var output []string
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			output = append(output, trimmed)
		}
	}
	return output
}
