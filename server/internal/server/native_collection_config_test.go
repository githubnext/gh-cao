package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func TestNativeCollectionConfigurationHasFeatureSpecificDependencies(t *testing.T) {
	config := CollectorConfig{NativeAcquisition: true, AppID: 7, PrivateKeyPEM: []byte("validated by App adapter"),
		ControlRepository: "octo/api", NativeInventoryDigest: strings.Repeat("a", 64),
		CatalogRoot: "/must-not-read", PolicyPath: "/must-not-read"}
	if err := config.validateNative(); err != nil || config.QueueMaxLength != defaultQueueMaxLength {
		t.Fatalf("native configuration required Value files without Value enabled: %v", err)
	}
	admission := CollectorConfig{NativeAcquisition: true, AppID: 7, AdmitOnly: true}
	if err := admission.validateNative(); err != nil {
		t.Fatalf("native admission required worker credentials or inventory: %v", err)
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*CollectorConfig)
	}{
		{"missing-app", func(c *CollectorConfig) { c.AppID = 0 }},
		{"missing-key", func(c *CollectorConfig) { c.PrivateKeyPEM = nil }},
		{"missing-control", func(c *CollectorConfig) { c.ControlRepository = "" }},
		{"missing-inventory", func(c *CollectorConfig) { c.NativeInventoryDigest = "" }},
		{"malformed-digest", func(c *CollectorConfig) { c.NativeInventoryDigest = "reviewed" }},
		{"uppercase-digest", func(c *CollectorConfig) { c.NativeInventoryDigest = strings.Repeat("A", 64) }},
		{"negative-workers", func(c *CollectorConfig) { c.Workers = -1 }},
		{"negative-window", func(c *CollectorConfig) { c.WindowDays = -1 }},
		{"negative-timeout", func(c *CollectorConfig) { c.CollectionTimeout = -time.Second }},
		{"negative-refresh", func(c *CollectorConfig) { c.RepositoryRefreshInterval = -time.Second }},
		{"missing-value-assets", func(c *CollectorConfig) { c.NativeValues, c.CatalogRoot = true, "" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			invalid := config
			testCase.mutate(&invalid)
			if err := invalid.validateNative(); err == nil {
				t.Fatal("invalid native configuration was silently accepted")
			}
		})
	}
	for _, testCase := range []struct {
		name   string
		mutate func(*CollectorConfig)
	}{
		{"run-limit", func(c *CollectorConfig) { c.RunLimit = 1 }},
		{"lake-limit", func(c *CollectorConfig) { c.MaxStorageMB = 1 }},
		{"request-minutes", func(c *CollectorConfig) { c.RequestTimeoutMinutes = 1 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			invalid := config
			testCase.mutate(&invalid)
			if err := invalid.validateNative(); !errors.Is(err, collectnative.ErrUnsupported) {
				t.Fatalf("legacy option was silently ignored: %v", err)
			}
		})
	}
	for _, mutate := range []func(*CollectorConfig){
		func(c *CollectorConfig) { c.PrivateKeyPEM = []byte("must-not-be-held") },
		func(c *CollectorConfig) { c.Workers = 1 },
		func(c *CollectorConfig) { c.RecoverDeliveries = true },
		func(c *CollectorConfig) { c.NativeValues = true },
	} {
		invalid := admission
		mutate(&invalid)
		if err := invalid.validateNative(); err == nil {
			t.Fatal("admission-only acquired a worker capability")
		}
	}
}
