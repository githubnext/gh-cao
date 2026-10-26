package sessionarchive

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

const DuckDBVersion = "v1.4.3"

type Config struct {
	Enabled, DurableStorage                         bool
	Directory, Namespace, DuckDBPath                string
	Repositories                                    []string
	Policy                                          adxtrace.Policy
	MaxTraceBytes, MaxStagingBytes, MaxArchiveBytes int64
	MaxEntries                                      int
	Retention, ReplayHorizon, QueryTimeout          time.Duration
}

func FromEnv(get func(string) string) (Config, error) {
	var c Config
	if value := get("CAO_SESSION_ARCHIVE_ENABLED"); value == "" || value == "false" {
		return c, nil
	} else if value != "true" {
		return c, errors.New("CAO_SESSION_ARCHIVE_ENABLED must be true or false")
	}
	c.Enabled, c.DurableStorage = true, get("CAO_SESSION_ARCHIVE_DURABLE_STORAGE") == "true"
	c.Directory, c.Namespace, c.DuckDBPath = get("CAO_SESSION_ARCHIVE_DIRECTORY"), get("CAO_SESSION_ARCHIVE_NAMESPACE"), get("CAO_SESSION_ARCHIVE_DUCKDB")
	c.Repositories = strings.Split(get("CAO_SESSION_ARCHIVE_REPOSITORIES"), ",")
	if mode := get("CAO_SESSION_ARCHIVE_MODE"); mode != "" && mode != "full" {
		return Config{}, errors.New("session archive currently supports full capture only; sampled capture is unavailable")
	}
	path := get("CAO_SESSION_ARCHIVE_POLICY")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return Config{}, errors.New("session archive policy must be a private bounded regular file")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return Config{}, err
	}
	defer func() { _ = root.Close() }()
	content, err := privateRead(root, filepath.Base(path), 1<<20)
	if err != nil {
		return Config{}, err
	}
	if err := decode(content, &c.Policy); err != nil {
		return Config{}, err
	}
	for _, field := range []struct {
		name string
		to   *int64
	}{{"MAX_TRACE_BYTES", &c.MaxTraceBytes}, {"MAX_STAGING_BYTES", &c.MaxStagingBytes}, {"MAX_ARCHIVE_BYTES", &c.MaxArchiveBytes}} {
		if value := get("CAO_SESSION_ARCHIVE_" + field.name); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return Config{}, err
			}
			*field.to = n
		}
	}
	if value := get("CAO_SESSION_ARCHIVE_MAX_ENTRIES"); value != "" {
		c.MaxEntries, err = strconv.Atoi(value)
		if err != nil {
			return Config{}, err
		}
	}
	for _, field := range []struct {
		name string
		to   *time.Duration
	}{{"RETENTION", &c.Retention}, {"REPLAY_HORIZON", &c.ReplayHorizon}, {"QUERY_TIMEOUT", &c.QueryTimeout}} {
		if value := get("CAO_SESSION_ARCHIVE_" + field.name); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, err
			}
			*field.to = d
		}
	}
	return c, c.Validate()
}

var repositoryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$`)

func validIdentity(namespace, repository, run string, attempt int64) bool {
	n, err := strconv.ParseUint(run, 10, 64)
	return namespace != "" && len(namespace) <= 256 && repositoryPattern.MatchString(repository) &&
		err == nil && n > 0 && strconv.FormatUint(n, 10) == run && attempt > 0
}

func (c Config) traceConfig() adxtrace.Config {
	return adxtrace.Config{Enabled: c.Enabled, DurableStorage: c.DurableStorage, Directory: c.Directory,
		Repositories: c.Repositories, Policy: c.Policy, Mode: "full", MaxTraceBytes: c.MaxTraceBytes,
		MaxSpoolBytes: c.MaxStagingBytes, MaxEntries: c.MaxEntries}
}

func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.MaxTraceBytes == 0 {
		c.MaxTraceBytes = 8 << 20
	}
	if c.MaxStagingBytes == 0 {
		c.MaxStagingBytes = 128 << 20
	}
	if c.MaxArchiveBytes == 0 {
		c.MaxArchiveBytes = 4 << 30
	}
	if c.MaxEntries == 0 {
		c.MaxEntries = 10000
	}
	if c.Retention == 0 {
		c.Retention = 30 * 24 * time.Hour
	}
	if c.ReplayHorizon == 0 {
		c.ReplayHorizon = 365 * 24 * time.Hour
	}
	if c.QueryTimeout == 0 {
		c.QueryTimeout = 30 * time.Second
	}
	if c.Namespace == "" || len(c.Namespace) > 256 || c.MaxStagingBytes > 128<<20 ||
		c.MaxArchiveBytes < 2*c.MaxStagingBytes || c.MaxArchiveBytes > 1<<40 ||
		c.ReplayHorizon < c.Retention || c.ReplayHorizon > 365*24*time.Hour ||
		c.Retention < 24*time.Hour || c.QueryTimeout < time.Second || c.QueryTimeout > 2*time.Minute {
		return errors.New("invalid session archive identity, resource, or retention configuration")
	}
	trace := c.traceConfig()
	if err := trace.Validate(); err != nil {
		return err
	}
	c.Repositories = trace.Repositories
	content, err := json.Marshal(c.Policy)
	if err != nil {
		return err
	}
	return decode(content, &c.Policy)
}
