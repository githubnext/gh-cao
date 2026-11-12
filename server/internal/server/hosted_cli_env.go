package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

// HostedCLIConfigFromEnv builds the admission configuration enabled by
// `serve-hosted --experimental --hosted-cli-actions`.
func HostedCLIConfigFromEnv(catalogPath string) (HostedCLIConfig, error) {
	catalog, err := hostedcli.LoadCatalog(catalogPath)
	if err != nil {
		return HostedCLIConfig{}, err
	}
	dashboardURL := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_DASHBOARD_URL"))
	if dashboardURL == "" {
		dashboardURL = dashboardOrigin(os.Getenv("CAO_GITHUB_REDIRECT_URL"))
	}
	return HostedCLIConfig{
		Catalog:           catalog,
		ControlRepository: hostedCLIControlRepository(),
		GitHubURL:         strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_GITHUB_URL")),
		DashboardURL:      dashboardURL,
	}, nil
}

func dashboardOrigin(redirect string) string {
	parsed, err := url.Parse(strings.TrimSpace(redirect))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return "https://" + parsed.Host + "/"
}

func hostedCLIControlRepository() string {
	if value := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_CONTROL_REPOSITORY")); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("CAO_COLLECT_CONTROL_REPOSITORY"))
}

// HostedCLIExecutorOptions are the explicit command-line choices for
// `execute-cli`.
type HostedCLIExecutorOptions struct {
	Owner               string
	AcknowledgeRecovery bool
	Prefetch            int
	MaxPooledTokens     int
	// LocalDevelopment explicitly permits development-only container runtimes.
	// It does not change PostgreSQL storage or credential requirements.
	LocalDevelopment bool
	Logger           *log.Logger
}

// HostedCLIExecutor owns an executor and the resources it opened.
type HostedCLIExecutor struct {
	*hostedcli.Executor
	store io.Closer
}

// Close releases the operational store.
func (e *HostedCLIExecutor) Close() error { return e.store.Close() }

// NewHostedCLIExecutorFromEnv builds the queue executor. It uses a dedicated
// GitHub App (CAO_HOSTED_CLI_APP_ID) so the read-only collector App never
// gains write permissions.
func NewHostedCLIExecutorFromEnv(ctx context.Context, options HostedCLIExecutorOptions) (*HostedCLIExecutor, error) {
	rawAppID := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_APP_ID"))
	appID, err := strconv.ParseInt(rawAppID, 10, 64)
	if err != nil || appID <= 0 {
		return nil, errors.New("hosted CLI execution requires CAO_HOSTED_CLI_APP_ID as a positive GitHub App identifier")
	}
	privateKey, err := privateKeyFromEnv("CAO_HOSTED_CLI_PRIVATE_KEY", "hosted CLI execution")
	if err != nil {
		return nil, err
	}
	apiURL := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_GITHUB_API_URL"))
	client, err := githubapp.New(githubapp.Config{AppID: appID, PrivateKeyPEM: privateKey, BaseURL: apiURL})
	if err != nil {
		return nil, err
	}
	control := hostedCLIControlRepository()
	if !hostedcli.ValidRepository(control) {
		return nil, errors.New("hosted CLI execution requires CAO_HOSTED_CLI_CONTROL_REPOSITORY as OWNER/REPOSITORY")
	}
	store, err := StoreFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	services := store.OperationalServices()
	if store.Capabilities().Revocations.Persistence != operational.PersistenceRestart {
		_ = store.Close()
		return nil, errors.New("hosted CLI execution requires restart-durable token revocations")
	}
	executor, err := newHostedCLIExecutor(ctx, services.CLICommands, services.Audit, services.Revocations, client, control, apiURL, options)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return &HostedCLIExecutor{Executor: executor, store: store}, nil
}

func newHostedCLIExecutor(ctx context.Context, queues operational.CLICommandQueues, auditStore operational.AuditLog, revocationQueue operational.RevocationQueue, client *githubapp.Client, control, apiURL string, options HostedCLIExecutorOptions) (*hostedcli.Executor, error) {
	if queues == nil {
		return nil, errors.New("hosted CLI execution requires the postgres operational store")
	}
	runner, err := newHostedCLIRunnerFromEnv(options)
	if err != nil {
		return nil, err
	}
	queueOptions, err := hostedCLIQueueOptions(options.LocalDevelopment)
	if err != nil {
		return nil, err
	}
	queue, err := queues.OpenCLICommandQueue(ctx, hostedcli.QueueName, queueOptions)
	if err != nil {
		return nil, errors.New("open hosted CLI command queue failed")
	}
	host, err := gitHubHost(apiURL)
	if err != nil {
		return nil, err
	}
	workRoot := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_WORK_ROOT"))
	if auditStore == nil {
		return nil, errors.New("hosted CLI execution requires the postgres operational audit table")
	}
	auditLog, err := audit.FromEnv()
	if err != nil {
		return nil, err
	}
	auditLog = auditLog.WithStore(auditStore)
	revocations, err := hostedCLIRevocationsFromEnv(revocationQueue)
	if err != nil {
		return nil, err
	}
	broker := hostedcli.NewBroker(client, hostedcli.BrokerOptions{MaxPooled: options.MaxPooledTokens, Audit: auditLog, Revocations: revocations})
	executor, err := hostedcli.NewExecutor(queue, broker, client, runner, hostedcli.Options{
		Owner:               options.Owner,
		ControlRepository:   control,
		ContainerImage:      runner.Image,
		GitHubHost:          host,
		WorkRoot:            workRoot,
		CommandTimeout:      queueOptions.Timeout,
		Prefetch:            options.Prefetch,
		AcknowledgeRecovery: options.AcknowledgeRecovery,
		Logger:              options.Logger,
		Audit:               auditLog,
	})
	if err != nil {
		_ = broker.Close(ctx)
		return nil, err
	}
	return executor, nil
}

// gitHubHost derives the gh hostname from an App API base URL.
func gitHubHost(apiURL string) (string, error) {
	if apiURL == "" {
		return "github.com", nil
	}
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("CAO_HOSTED_CLI_GITHUB_API_URL must be an HTTPS URL")
	}
	host := strings.ToLower(parsed.Host)
	if host == "api.github.com" {
		return "github.com", nil
	}
	return strings.TrimPrefix(host, "api."), nil
}

// privateKeyFromEnv prefers PREFIX_FILE, then PREFIX_BASE64, then the inline
// PREFIX value.
func privateKeyFromEnv(prefix, purpose string) ([]byte, error) {
	if path := strings.TrimSpace(os.Getenv(prefix + "_FILE")); path != "" {
		// #nosec G304,G703 -- the operator explicitly configures the private-key file path.
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s_FILE: %w", prefix, err)
		}
		return content, nil
	}
	if encoded := strings.TrimSpace(os.Getenv(prefix + "_BASE64")); encoded != "" {
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode %s_BASE64: %w", prefix, err)
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			return nil, fmt.Errorf("%s_BASE64 decodes to an empty private key", prefix)
		}
		return content, nil
	}
	inline := strings.TrimSpace(os.Getenv(prefix))
	if inline == "" {
		return nil, fmt.Errorf("%s requires %s_FILE, %s_BASE64, or %s", purpose, prefix, prefix, prefix)
	}
	return []byte(inline), nil
}
