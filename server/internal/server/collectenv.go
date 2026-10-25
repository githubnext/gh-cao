package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var collectEnvLog = logger.New("cao:server:collectenv")

// collectorProfile identifies which of the two mutually exclusive
// collection profiles CollectorConfigFromEnv selected. It is useful for
// diagnosing a misconfigured deployment without logging any of the
// resolved configuration values themselves.
type collectorProfile string

const (
	collectorProfileActionsSnapshot collectorProfile = "actions-snapshot"
	collectorProfileCollection      collectorProfile = "collection"
)

// classifyCollectorProfile decides which collection profile a raw
// CAO_COLLECT_APP_ID value selects: a blank value selects the default
// published-snapshot Actions profile, and any other value selects the
// collection profile. It is a pure function extracted from
// CollectorConfigFromEnv so this branch is independently testable without
// touching the process environment or requiring a valid App identifier.
func classifyCollectorProfile(rawAppID string) collectorProfile {
	if strings.TrimSpace(rawAppID) == "" {
		return collectorProfileActionsSnapshot
	}
	return collectorProfileCollection
}

// CollectorConfigFromEnv reads the optional collection profile from the
// environment.
//
// It returns nil when CAO_COLLECT_APP_ID is unset, which selects the default
// Actions profile. Secrets are read from the environment only; they are never
// written to policy, logs, or status.
//
//nolint:nilnil // a nil config with a nil error is the documented "Actions profile selected" result.
func CollectorConfigFromEnv() (*CollectorConfig, error) {
	rawAppID := strings.TrimSpace(os.Getenv("CAO_COLLECT_APP_ID"))
	profile := classifyCollectorProfile(rawAppID)
	if profile == collectorProfileActionsSnapshot {
		collectEnvLog.Printf("collector profile resolved profile=%s", profile)
		return nil, nil
	}
	appID, err := strconv.ParseInt(rawAppID, 10, 64)
	if err != nil || appID <= 0 {
		return nil, errors.New("CAO_COLLECT_APP_ID must be a positive GitHub App identifier")
	}
	admitOnly := envBool("CAO_COLLECT_ADMIT_ONLY")
	var privateKey []byte
	if !admitOnly {
		key, err := collectorPrivateKey()
		if err != nil {
			return nil, err
		}
		privateKey = key
	}
	collectEnvLog.Printf("collector profile resolved profile=%s admit_only=%t", profile, admitOnly)
	config := &CollectorConfig{
		AppID:                     appID,
		AdmitOnly:                 admitOnly,
		PrivateKeyPEM:             privateKey,
		BaseURL:                   strings.TrimSpace(os.Getenv("CAO_COLLECT_GITHUB_API_URL")),
		UploadURL:                 strings.TrimSpace(os.Getenv("CAO_COLLECT_GITHUB_UPLOAD_URL")),
		LakeDirectory:             strings.TrimSpace(os.Getenv("CAO_COLLECT_LAKE_DIRECTORY")),
		CatalogRoot:               strings.TrimSpace(os.Getenv("CAO_COLLECT_CATALOG_ROOT")),
		ControlRepository:         strings.TrimSpace(os.Getenv("CAO_COLLECT_CONTROL_REPOSITORY")),
		PolicyPath:                strings.TrimSpace(os.Getenv("CAO_POLICY_PATH")),
		StaticInventoryPath:       strings.TrimSpace(os.Getenv("CAO_COLLECT_STATIC_INVENTORY")),
		NodeBinary:                strings.TrimSpace(os.Getenv("CAO_COLLECT_NODE_BINARY")),
		GitHubBinary:              strings.TrimSpace(os.Getenv("CAO_COLLECT_GH_BINARY")),
		Consumer:                  strings.TrimSpace(os.Getenv("CAO_COLLECT_CONSUMER")),
		WindowDays:                envInt("CAO_COLLECT_WINDOW_DAYS"),
		RunLimit:                  envInt("CAO_COLLECT_RUN_LIMIT"),
		MaxStorageMB:              envInt("CAO_COLLECT_MAX_STORAGE_MB"),
		RequestTimeoutMinutes:     envInt("CAO_COLLECT_REQUEST_TIMEOUT_MINUTES"),
		RateLimitFloor:            envInt("CAO_COLLECT_RATE_LIMIT_FLOOR"),
		Workers:                   envInt("CAO_COLLECT_WORKERS"),
		QueueMaxLength:            envInt("CAO_COLLECT_QUEUE_MAX_LENGTH"),
		InventoryLimit:            envInt("CAO_COLLECT_INVENTORY_LIMIT"),
		MinProjectionInterval:     envDuration("CAO_COLLECT_PROJECTION_INTERVAL"),
		CollectionTimeout:         envDuration("CAO_COLLECT_TIMEOUT"),
		RecoverDeliveries:         envBool("CAO_COLLECT_RECOVER_DELIVERIES"),
		RepositoryRefreshInterval: envStrictDuration("CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL"),
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// collectorPrivateKey prefers a mounted secret file, then a single-line base64
// value for hosts that cannot preserve multiline environment variables, then
// the inline PEM used by deployments that do preserve it.
func collectorPrivateKey() ([]byte, error) {
	if path := strings.TrimSpace(os.Getenv("CAO_COLLECT_PRIVATE_KEY_FILE")); path != "" {
		// #nosec G304,G703 -- the operator explicitly configures the private-key file path.
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read CAO_COLLECT_PRIVATE_KEY_FILE: %w", err)
		}
		return content, nil
	}
	if encoded := strings.TrimSpace(os.Getenv("CAO_COLLECT_PRIVATE_KEY_BASE64")); encoded != "" {
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode CAO_COLLECT_PRIVATE_KEY_BASE64: %w", err)
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			return nil, errors.New("CAO_COLLECT_PRIVATE_KEY_BASE64 decodes to an empty private key")
		}
		return content, nil
	}
	inline := strings.TrimSpace(os.Getenv("CAO_COLLECT_PRIVATE_KEY"))
	if inline == "" {
		return nil, errors.New(
			"collection requires CAO_COLLECT_PRIVATE_KEY_FILE, CAO_COLLECT_PRIVATE_KEY_BASE64, or CAO_COLLECT_PRIVATE_KEY")
	}
	return []byte(inline), nil
}

func envInt(name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return 0
	}
	return value
}

func envDuration(name string) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return 0
	}
	return value
}

// envStrictDuration returns -1 for a malformed value so validation fails
// closed instead of silently applying a default.
func envStrictDuration(name string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return -1
	}
	return value
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// NewCollectorFromEnv assembles the collection profile for the worker roles,
// which run the same binary without serving the dashboard.
func NewCollectorFromEnv(ctx context.Context, databaseQueriesPath string) (*Collector, error) {
	if value := os.Getenv("CAO_ADX_TRACES_ENABLED"); value != "" && value != "false" {
		return nil, errors.New("unified traces require the OAuth server with co-resident collection workers, not standalone collect/backfill roles")
	}
	config, err := CollectorConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, errors.New("collection is not configured; CAO_COLLECT_APP_ID is required")
	}
	if config.AdmitOnly {
		return nil, errors.New(
			"the collect and backfill roles require collection credentials; " +
				"unset CAO_COLLECT_ADMIT_ONLY")
	}
	if strings.TrimSpace(os.Getenv("CAO_SOURCE_DIRECTORY")) != "" {
		return nil, errors.New(
			"collection and published-snapshot ingestion are alternatives: " +
				"unset CAO_SOURCE_DIRECTORY to run the collection profile")
	}
	store, err := StoreFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	postgresURL := strings.TrimSpace(os.Getenv("CAO_POSTGRES_URL"))
	if postgresURL == "" {
		return nil, errors.New("CAO_POSTGRES_URL is required")
	}
	host, err := loadHostPolicyFromEnv()
	if err != nil {
		return nil, err
	}
	databaseNamespace := strings.TrimSpace(host.RedisNamespace)
	if databaseNamespace == "" {
		databaseNamespace = "hosted-dashboard"
	}
	database, err := postgresx.OpenWithNamespace(ctx, postgresURL, databaseNamespace)
	if err != nil {
		return nil, errors.New("connect to dashboard Postgres failed")
	}
	collector, err := NewCollector(ctx, store, database, *config, databaseQueriesPath)
	if err != nil {
		_ = database.Close()
	}
	return collector, err
}
