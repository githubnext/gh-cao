package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
)

var environmentLog = logger.New("cao:server:environment")

type positiveInt64Setting struct {
	name string
	dest *int64
}

type positiveIntSetting struct {
	name string
	dest *int
}

// positiveEnvSetting reads an optional positive integer from the environment.
// An unset or blank variable reports set=false and leaves the caller's default.
func positiveEnvSetting(name string, bitSize int) (value int64, set bool, err error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, bitSize)
	if err != nil || n <= 0 {
		environmentLog.Printf("operational setting rejected name=%s", name)
		return 0, false, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, true, nil
}

func applyInt64Settings(settings []positiveInt64Setting) error {
	for _, setting := range settings {
		n, set, err := positiveEnvSetting(setting.name, 64)
		if err != nil {
			return err
		}
		if set {
			*setting.dest = n
		}
	}
	return nil
}

func applyIntSettings(settings []positiveIntSetting) error {
	for _, setting := range settings {
		n, set, err := positiveEnvSetting(setting.name, 0)
		if err != nil {
			return err
		}
		if set {
			*setting.dest = int(n)
		}
	}
	return nil
}

// OperationalEnvironment describes reviewed storage selection without opening
// an adapter. Namespace is operational; DatabaseNamespace preserves the
// existing canonical Postgres dataset identity.
type OperationalEnvironment struct {
	Backend, Namespace, DatabaseNamespace string
	RedisURL                              string
	SingleProcess, AllowVolatile          bool
}

func OperationalEnvironmentFromEnv() (OperationalEnvironment, error) {
	host, err := loadHostPolicyFromEnv()
	if err != nil {
		return OperationalEnvironment{}, err
	}
	return operationalEnvironment(host), nil
}

// OperationalPolicySelectedFromEnv identifies an explicit reviewed selector
// without opening an adapter or requiring provider credentials.
func OperationalPolicySelectedFromEnv() (bool, error) {
	path := configuredHostPolicyPath()
	if _, err := os.Stat(path); os.IsNotExist(err) &&
		strings.TrimSpace(os.Getenv("CAO_POLICY_PATH")) == "" &&
		strings.TrimSpace(os.Getenv(marketplacePolicyPathEnv)) == "" {
		return false, nil
	}
	document, err := loadComposedHostPolicy(path)
	if err != nil {
		return false, err
	}
	control, _ := document["control-plane"].(map[string]any)
	web, _ := control["web"].(map[string]any)
	host, _ := web["host"].(map[string]any)
	_, selected := host["operational-store"]
	return selected, nil
}

func memoryConfigFromEnv() (memory.Config, error) {
	config := memory.Config{}
	if err := applyInt64Settings([]positiveInt64Setting{
		{"CAO_OPERATIONAL_CACHE_MAX_BYTES", &config.MaxCacheBytes},
		{"CAO_OPERATIONAL_CACHE_MAX_VALUE_BYTES", &config.MaxCacheValueBytes},
		{"CAO_OPERATIONAL_PROTECTED_MAX_BYTES", &config.MaxProtectedBytes},
		{"CAO_OPERATIONAL_TASK_MAX_BYTES", &config.MaxTaskBytes},
	}); err != nil {
		return config, err
	}
	if err := applyIntSettings([]positiveIntSetting{
		{"CAO_OPERATIONAL_CACHE_MAX_ENTRIES", &config.MaxCacheEntries},
		{"CAO_OPERATIONAL_PROTECTED_MAX_ENTRIES", &config.MaxProtectedEntries},
		{"CAO_OPERATIONAL_MAX_QUEUED_TASKS", &config.MaxQueuedTasks},
		{"CAO_OPERATIONAL_MAX_RATE_LIMIT_SUBJECTS", &config.MaxRateLimitSubjects},
	}); err != nil {
		return config, err
	}
	defaults := memory.DefaultConfig()
	if config.MaxCacheBytes > 0 && config.MaxCacheValueBytes == 0 {
		config.MaxCacheValueBytes = min(config.MaxCacheBytes, defaults.MaxCacheValueBytes)
	}
	if config.MaxProtectedBytes > 0 {
		config.MaxMetadataBytes = min(config.MaxProtectedBytes, defaults.MaxMetadataBytes)
	}
	if config.MaxProtectedEntries > 0 {
		config.MaxMetadataEntries = min(config.MaxProtectedEntries, defaults.MaxMetadataEntries)
	}
	return config, nil
}

func operationalEnvironment(host *resolvedHostPolicy) OperationalEnvironment {
	return OperationalEnvironment{
		Backend: host.OperationalBackend, Namespace: host.OperationalNamespace,
		RedisURL:          host.RedisURL,
		DatabaseNamespace: host.DatabaseNamespace, SingleProcess: host.Profile.SingleProcess,
		AllowVolatile: host.AllowVolatile,
	}
}

func PostgresOperationalConfigFromEnv() (postgres.Config, error) {
	config := postgres.Config{}
	if err := applyInt64Settings([]positiveInt64Setting{
		{"CAO_OPERATIONAL_CACHE_MAX_BYTES", &config.MaxCacheBytes},
		{"CAO_OPERATIONAL_CACHE_MAX_VALUE_BYTES", &config.MaxCacheValueBytes},
		{"CAO_OPERATIONAL_CACHE_MAX_ENTRIES", &config.MaxCacheEntries},
		{"CAO_OPERATIONAL_PROTECTED_MAX_BYTES", &config.MaxProtectedBytes},
		{"CAO_OPERATIONAL_PROTECTED_MAX_ENTRIES", &config.MaxProtectedEntries},
	}); err != nil {
		return config, err
	}
	if config.MaxCacheBytes > 0 && config.MaxCacheValueBytes == 0 {
		config.MaxCacheValueBytes = min(config.MaxCacheBytes, 4<<20)
	}
	return config, nil
}
