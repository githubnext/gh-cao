// Package adxtrace captures approved unified evidence independently of dashboard retention.
package adxtrace

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Policy struct {
	Version         string `json:"version"`
	ContentApproved bool   `json:"contentApproved"`
	RightsApproved  bool   `json:"rightsApproved"`
	MiningApproved  bool   `json:"miningApproved,omitempty"`
	// Each field admits its entire JSON subtree, not unspecified sibling fields.
	Events         map[string][]string `json:"events"`
	RedactPatterns []string            `json:"redactPatterns"`
}

type Config struct {
	Enabled        bool
	Directory      string
	DurableStorage bool
	Repositories   []string
	Mode           string
	SampleCount    int
	Seed           string
	FrameDelay     time.Duration
	MaxTraceBytes  int64
	MaxSpoolBytes  int64
	MaxEntries     int
	Retention      time.Duration
	Policy         Policy
	MiningEnabled  bool
	MiningInterval time.Duration
}

func FromEnv(get func(string) string) (Config, error) {
	var c Config
	if value := get("CAO_ADX_TRACES_ENABLED"); value == "" || value == "false" {
		return c, nil
	} else if value != "true" {
		return c, errors.New("CAO_ADX_TRACES_ENABLED must be true or false")
	}
	c.Enabled, c.Directory = true, get("CAO_ADX_TRACE_DIRECTORY")
	if value := get("CAO_ADX_TRACE_MINING_ENABLED"); value != "" && value != "false" {
		if value != "true" {
			return Config{}, errors.New("CAO_ADX_TRACE_MINING_ENABLED must be true or false")
		}
		c.MiningEnabled = true
	}
	c.DurableStorage = get("CAO_ADX_TRACE_DURABLE_STORAGE") == "true"
	c.Repositories = strings.Split(get("CAO_ADX_TRACE_REPOSITORIES"), ",")
	c.Mode, c.Seed = get("CAO_ADX_TRACE_MODE"), get("CAO_ADX_TRACE_SEED")
	for _, item := range []struct {
		name string
		to   *int64
	}{{"CAO_ADX_TRACE_MAX_BYTES", &c.MaxTraceBytes}, {"CAO_ADX_TRACE_SPOOL_BYTES", &c.MaxSpoolBytes}} {
		if value := get(item.name); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return Config{}, errors.New("invalid trace byte budget")
			}
			*item.to = n
		}
	}
	if c.Mode == "sample" && get("CAO_ADX_TRACE_SAMPLE_COUNT") == "" {
		return Config{}, errors.New("sample mode requires an explicit trace sample count")
	}
	for _, item := range []struct {
		name string
		to   *int
	}{{"CAO_ADX_TRACE_SAMPLE_COUNT", &c.SampleCount}, {"CAO_ADX_TRACE_MAX_ENTRIES", &c.MaxEntries}} {
		if value := get(item.name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return Config{}, errors.New("invalid trace sample count or entry budget")
			}
			*item.to = n
		}
	}
	for _, item := range []struct {
		name string
		to   *time.Duration
	}{{"CAO_ADX_TRACE_RETENTION", &c.Retention}, {"CAO_ADX_TRACE_FRAME_DELAY", &c.FrameDelay},
		{"CAO_ADX_TRACE_MINING_INTERVAL", &c.MiningInterval}} {
		if value := get(item.name); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, errors.New("invalid trace duration")
			}
			*item.to = d
		}
	}
	policy, err := readPrivateFile(get("CAO_ADX_TRACE_POLICY"), 1<<20)
	if err != nil {
		return Config{}, errors.New("trace policy must be a private bounded regular file")
	}
	if err := decode(policy, &c.Policy); err != nil {
		return Config{}, errors.New("invalid trace content policy")
	}
	return c, c.Validate()
}

var repositoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$`)
var fieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,99}$`)
var eventName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]{0,127}$`)

func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if runtime.GOOS == "windows" {
		return errors.New("unified trace spooling requires a POSIX host")
	}
	if c.Directory == "" || !c.DurableStorage {
		return errors.New("traces require an explicitly confirmed private durable spool")
	}
	if c.Mode != "full" && c.Mode != "sample" {
		return errors.New("trace mode must explicitly select full or sample")
	}
	if len(c.Repositories) == 0 || len(c.Repositories) > 1000 {
		return errors.New("traces require exact repository scope")
	}
	c.Repositories = slices.Clone(c.Repositories)
	for i, repo := range c.Repositories {
		c.Repositories[i] = strings.ToLower(strings.TrimSpace(repo))
		if !repositoryName.MatchString(c.Repositories[i]) {
			return errors.New("trace scope must contain exact owner/repository names")
		}
	}
	slices.Sort(c.Repositories)
	if len(slices.Compact(slices.Clone(c.Repositories))) != len(c.Repositories) {
		return errors.New("duplicate trace repository")
	}
	if c.Mode == "sample" && (len(c.Seed) < 16 || c.SampleCount < 0 || c.SampleCount > 100000) {
		return errors.New("sampling requires a recorded seed of at least 16 bytes and a bounded sample count")
	}
	if c.MaxTraceBytes == 0 {
		c.MaxTraceBytes = 8 << 20
	}
	if c.MaxSpoolBytes == 0 {
		c.MaxSpoolBytes = 256 << 20
	}
	if c.MaxEntries == 0 {
		c.MaxEntries = 10000
	}
	if c.Retention == 0 {
		c.Retention = 7 * 24 * time.Hour
	}
	if c.FrameDelay == 0 {
		c.FrameDelay = 24 * time.Hour
	}
	if c.MiningInterval == 0 {
		c.MiningInterval = 5 * time.Minute
	}
	if c.MiningEnabled && (!c.Policy.MiningApproved || c.MiningInterval < time.Minute || c.MiningInterval > 24*time.Hour) {
		return errors.New("mining requires explicit evidence approval and a 1m-24h refresh interval")
	}
	if c.MaxTraceBytes < 1024 || c.MaxTraceBytes > 32<<20 ||
		c.MaxSpoolBytes < 2*c.MaxTraceBytes || c.MaxSpoolBytes > 1<<40 ||
		c.MaxEntries < 1 || c.MaxEntries > 100000 ||
		c.Retention < 24*time.Hour || c.Retention > 365*24*time.Hour ||
		c.FrameDelay < time.Hour || c.FrameDelay > 7*24*time.Hour || c.Retention <= c.FrameDelay {
		return errors.New("invalid trace resource or retention budgets")
	}
	if !c.Policy.ContentApproved || !c.Policy.RightsApproved || c.Policy.Version == "" ||
		len(c.Policy.Version) > 100 || len(c.Policy.Events) == 0 || len(c.Policy.Events) > 256 {
		return errors.New("trace content and rights approval and a versioned closed event policy are required")
	}
	for event, fields := range c.Policy.Events {
		if !eventName.MatchString(event) || len(fields) > 256 {
			return errors.New("invalid trace event policy")
		}
		for _, field := range fields {
			if !fieldName.MatchString(field) {
				return errors.New("invalid approved trace field")
			}
		}
	}
	if len(c.Policy.RedactPatterns) > 32 {
		return errors.New("too many trace redaction patterns")
	}
	for _, pattern := range c.Policy.RedactPatterns {
		if len(pattern) > 4096 {
			return errors.New("trace redaction pattern is too large")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return errors.New("invalid trace redaction pattern")
		}
	}
	return nil
}

func readPrivateFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("private regular file required")
	}
	// OpenRoot confines the final open even if a directory is replaced by a symlink.
	root, err := os.OpenRoot(parent(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return readBounded(root, base(path), limit)
}

func policyDigest(p Policy) string {
	content, _ := json.Marshal(struct {
		Transform string
		Policy    Policy
	}{"cao-approved-events-v1", p})
	return digest(content)
}
