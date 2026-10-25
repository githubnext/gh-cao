// Package adxingest implements CAO's optional ADX Free-compatible exporter.
package adxingest

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,99}$`)
var tenantID = regexp.MustCompile(`^[a-fA-F0-9-]{36}$`)

type Config struct {
	Enabled         bool
	Endpoint        string
	Database        string
	TenantID        string
	ClientID        string
	ClientSecret    string
	RepositoryIDs   []string
	NonPersonalData bool
	SingleReplica   bool
	Interval        time.Duration
	// MaxStorageBytes is a conservative total OriginalSize budget across
	// the six destination tables, not a compression estimate or Azure quota.
	MaxStorageBytes int64
}

func FromEnv(getenv func(string) string) (Config, error) {
	var config Config
	enabled := getenv("CAO_ADX_ENABLED")
	if enabled == "" || enabled == "false" {
		return config, nil
	}
	if enabled != "true" {
		return config, errors.New("CAO_ADX_ENABLED must be true or false")
	}
	config = Config{
		Enabled: true, Endpoint: getenv("CAO_ADX_ENDPOINT"), Database: getenv("CAO_ADX_DATABASE"),
		TenantID: getenv("CAO_ADX_TENANT_ID"), ClientID: getenv("CAO_ADX_CLIENT_ID"),
		ClientSecret: getenv("CAO_ADX_CLIENT_SECRET"), NonPersonalData: getenv("CAO_ADX_NON_PERSONAL_DATA") == "true",
		SingleReplica: getenv("CAO_ADX_SINGLE_REPLICA") == "true",
	}
	config.RepositoryIDs = strings.Split(getenv("CAO_ADX_REPOSITORY_IDS"), ",")
	for i := range config.RepositoryIDs {
		config.RepositoryIDs[i] = strings.TrimSpace(config.RepositoryIDs[i])
	}
	if value := getenv("CAO_ADX_INTERVAL"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, errors.New("CAO_ADX_INTERVAL must be a duration")
		}
		config.Interval = duration
	}
	if value := getenv("CAO_ADX_MAX_STORAGE_BYTES"); value != "" {
		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Config{}, errors.New("CAO_ADX_MAX_STORAGE_BYTES must be an integer")
		}
		config.MaxStorageBytes = bytes
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil ||
		endpoint.Port() != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Path != "" && endpoint.Path != "/") ||
		!strings.HasSuffix(endpoint.Hostname(), ".kusto.windows.net") ||
		strings.HasPrefix(endpoint.Hostname(), "ingest-") {
		return errors.New("ADX requires a public-cloud HTTPS Kusto query endpoint without credentials, port or path")
	}
	c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	if !identifier.MatchString(c.Database) || !tenantID.MatchString(c.TenantID) ||
		!tenantID.MatchString(c.ClientID) || strings.TrimSpace(c.ClientSecret) == "" {
		return errors.New("ADX requires a database identifier and explicit Entra application credentials")
	}
	if !c.NonPersonalData {
		return errors.New("ADX Free requires explicit approval of non-personal, non-regulated data")
	}
	if !c.SingleReplica {
		return errors.New("ADX v1 requires CAO_ADX_SINGLE_REPLICA=true and exactly one exporter replica")
	}
	if len(c.RepositoryIDs) == 0 || len(c.RepositoryIDs) > 1000 {
		return errors.New("ADX requires 1-1000 exact canonical repository IDs")
	}
	c.RepositoryIDs = slices.Clone(c.RepositoryIDs)
	slices.Sort(c.RepositoryIDs)
	for i, id := range c.RepositoryIDs {
		if strings.TrimSpace(id) == "" || len(id) > 512 || i > 0 && id == c.RepositoryIDs[i-1] {
			return errors.New("ADX repository scope must be nonempty, bounded and unique")
		}
	}
	if c.Interval == 0 {
		c.Interval = 30 * time.Second
	}
	if c.Interval < time.Second || c.Interval > time.Hour {
		return errors.New("ADX interval must be between one second and one hour")
	}
	if c.MaxStorageBytes == 0 {
		c.MaxStorageBytes = 50_000_000_000
	}
	if c.MaxStorageBytes < 1<<20 || c.MaxStorageBytes > 50_000_000_000 {
		return errors.New("ADX storage budget must be between 1 MiB and 50 GB")
	}
	return nil
}
