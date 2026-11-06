package server

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

// ApplyNativeEnvironment reads non-secret native feature settings through the
// supplied lookup so runtime configuration and diagnostics use the same rules.
func (config *CollectorConfig) ApplyNativeEnvironment(lookup func(string) string) error {
	for _, field := range []struct {
		name  string
		value *bool
	}{
		{"CAO_COLLECT_NATIVE", &config.NativeAcquisition},
		{"CAO_COLLECT_NATIVE_VALUES", &config.NativeValues},
	} {
		value, err := collectorFeatureBoolWithLookup(field.name, lookup)
		if err != nil {
			return err
		}
		*field.value = value
	}
	config.NativeInventoryDigest = strings.TrimSpace(lookup("CAO_COLLECT_NATIVE_INVENTORY_DIGEST"))
	if !config.NativeAcquisition {
		return nil
	}
	if err := rejectRetiredCollectorEnvironment(lookup); err != nil {
		return err
	}
	admitOnly, err := collectorFeatureBoolWithLookup("CAO_COLLECT_ADMIT_ONLY", lookup)
	if err != nil {
		return err
	}
	config.AdmitOnly = admitOnly
	for _, field := range []struct {
		name  string
		value *int
	}{
		{"CAO_COLLECT_WORKERS", &config.Workers},
		{"CAO_COLLECT_WINDOW_DAYS", &config.WindowDays},
		{"CAO_COLLECT_INVENTORY_LIMIT", &config.InventoryLimit},
		{"CAO_COLLECT_QUEUE_MAX_LENGTH", &config.QueueMaxLength},
		{"CAO_COLLECT_RATE_LIMIT_FLOOR", &config.RateLimitFloor},
		{"CAO_COLLECT_RUN_LIMIT", &config.RunLimit},
		{"CAO_COLLECT_MAX_STORAGE_MB", &config.MaxStorageMB},
		{"CAO_COLLECT_REQUEST_TIMEOUT_MINUTES", &config.RequestTimeoutMinutes},
	} {
		if raw := strings.TrimSpace(lookup(field.name)); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("%s must be an integer", field.name)
			}
			*field.value = value
		}
	}
	for _, field := range []struct {
		name  string
		value *time.Duration
	}{
		{"CAO_COLLECT_PROJECTION_INTERVAL", &config.MinProjectionInterval},
		{"CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL", &config.RepositoryRefreshInterval},
		{"CAO_COLLECT_TIMEOUT", &config.CollectionTimeout},
	} {
		if raw := strings.TrimSpace(lookup(field.name)); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("%s must be a Go duration", field.name)
			}
			*field.value = value
		}
	}
	recover, err := collectorFeatureBoolWithLookup("CAO_COLLECT_RECOVER_DELIVERIES", lookup)
	if err != nil {
		return err
	}
	config.RecoverDeliveries = recover
	return nil
}

func rejectRetiredCollectorEnvironment(lookup func(string) string) error {
	for _, name := range []string{"CAO_COLLECT_LAKE_DIRECTORY", "CAO_COLLECT_STATIC_INVENTORY", "CAO_COLLECT_GH_BINARY", "CAO_COLLECT_NODE_BINARY"} {
		if strings.TrimSpace(lookup(name)) != "" {
			return fmt.Errorf("%s is unsupported by native collection", name)
		}
	}
	return nil
}

func (config *CollectorConfig) validateNative() error {
	if config.AppID <= 0 {
		return errors.New("native collection requires a GitHub App identifier")
	}
	if config.Workers < 0 || config.WindowDays < 0 || config.InventoryLimit < 0 ||
		config.QueueMaxLength < 0 || config.RateLimitFloor < 0 ||
		config.RepositoryRefreshInterval < 0 || config.CollectionTimeout < 0 || config.MinProjectionInterval < 0 {
		return errors.New("native collection limits and intervals must not be negative")
	}
	if config.RunLimit != 0 || config.MaxStorageMB != 0 || config.RequestTimeoutMinutes != 0 {
		return fmt.Errorf("%w: native collection does not use legacy RunLimit, MaxStorageMB or RequestTimeoutMinutes; use its bounded discovery contract and CollectionTimeout",
			collectnative.ErrUnsupported)
	}
	if config.NativeInventoryDigest != "" {
		if len(config.NativeInventoryDigest) != 64 {
			return errors.New("native inventory digest must be a lowercase SHA-256 digest")
		}
		digest, err := hex.DecodeString(config.NativeInventoryDigest)
		if err != nil || len(digest) != 32 || strings.ToLower(config.NativeInventoryDigest) != config.NativeInventoryDigest {
			return errors.New("native inventory digest must be a lowercase SHA-256 digest")
		}
	}
	if config.AdmitOnly {
		if len(config.PrivateKeyPEM) != 0 || config.Workers != 0 || config.RecoverDeliveries || config.NativeValues {
			return errors.New("native admission must not hold App credentials, run workers, execute Value or replay App deliveries")
		}
	} else {
		if len(config.PrivateKeyPEM) == 0 {
			return errors.New("native collection requires a GitHub App private key")
		}
		name, err := collect.NormalizeRepository(config.ControlRepository)
		if err != nil {
			return fmt.Errorf("native collection control repository: %w", err)
		}
		config.ControlRepository = name
		if config.NativeInventory == nil && config.NativeInventoryDigest == "" {
			return errors.New("native collection requires reviewed inventory bytes or its provisioned digest")
		}
		if config.NativeValues && (strings.TrimSpace(config.CatalogRoot) == "" || strings.TrimSpace(config.PolicyPath) == "") {
			return errors.New("selected native Value execution requires its reviewed catalog and policy")
		}
	}
	if config.NativeInventory != nil {
		if err := config.NativeInventory.Validate(); err != nil {
			return err
		}
		if config.NativeInventoryDigest != "" && config.NativeInventory.Digest != config.NativeInventoryDigest {
			return errors.New("native inventory bytes differ from the selected reviewed digest")
		}
	}
	if config.QueueMaxLength == 0 {
		config.QueueMaxLength = defaultQueueMaxLength
	}
	return nil
}
