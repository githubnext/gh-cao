// Package sessionarchiveblob implements the private, optional Blob archive.
// It does not use PostgreSQL, native ADX ingestion, or runtime provisioning.
package sessionarchiveblob

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	accountPattern    = regexp.MustCompile(`^[a-z0-9]{3,24}$`)
	containerPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])$`)
	componentPattern  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,99}$`)
	digestPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	uuidPattern       = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
)

const planVersion = "cao-archive-object-membership-v1"

var (
	ErrDisabled    = errors.New("hosted session archive is disabled")
	ErrScope       = errors.New("hosted session archive scope is not authorized")
	ErrLease       = errors.New("hosted session archive ownership is unavailable")
	ErrConflict    = errors.New("hosted session archive publication conflicted")
	ErrIntegrity   = errors.New("hosted session archive integrity verification failed")
	ErrCapacity    = errors.New("hosted session archive bounded capacity exceeded")
	ErrWithdrawn   = errors.New("hosted session archive evidence is withdrawn or superseded")
	ErrConformance = errors.New("hosted session archive requires destination-bound Azure conformance evidence")
	ErrQuery       = errors.New("hosted session archive query failed or returned incomplete evidence")
)

type Config struct {
	Enabled       bool
	Account       string
	Container     string
	Prefix        string
	Endpoint      string
	Database      string
	Table         string
	Namespace     string
	RepositoryIDs []string
	TenantID      string
	ClientID      string
	ClientSecret  string `json:"-"`
	DataApproved  bool
	SingleReplica bool
	// Acceptance is an explicit operator activation gate, not a mock-generated
	// assertion of Azure semantics. It must equal AcceptanceBinding after the
	// environment-dependent conformance test has passed at this destination.
	Acceptance string
	// ExternalPropertiesDigest is the reviewed SHA-256 of the exact Properties
	// string returned by ".show external table". It binds all storage paths,
	// managed identity, partition and format settings without guessing the
	// undocumented engine-specific JSON representation.
	ExternalPropertiesDigest string
	MaxFileBytes             int64
	MaxQueryBytes            int64
	MaxFiles                 int
	MaxMembers               int
	MaxStateBytes            int64
	QueryTimeout             time.Duration
	RetirementGrace          time.Duration
}

func FromEnv(getenv func(string) string) (Config, error) {
	flag := getenv("CAO_SESSION_ARCHIVE_BLOB_ENABLED")
	if flag == "" || flag == "false" {
		return Config{}, nil
	}
	if flag != "true" {
		return Config{}, errors.New("CAO_SESSION_ARCHIVE_BLOB_ENABLED must be true or false")
	}
	value := func(name string) string { return getenv("CAO_SESSION_ARCHIVE_BLOB_" + name) }
	c := Config{Enabled: true, Account: value("ACCOUNT"), Container: value("CONTAINER"), Prefix: value("PREFIX"),
		Endpoint: value("ADX_ENDPOINT"), Database: value("ADX_DATABASE"), Table: value("ADX_TABLE"),
		Namespace: value("NAMESPACE"), RepositoryIDs: strings.Split(value("REPOSITORY_IDS"), ","),
		TenantID: value("TENANT_ID"), ClientID: value("CLIENT_ID"), ClientSecret: value("CLIENT_SECRET"),
		DataApproved: value("DATA_APPROVED") == "true", SingleReplica: value("SINGLE_REPLICA") == "true",
		Acceptance: value("ACCEPTANCE"), ExternalPropertiesDigest: value("EXTERNAL_PROPERTIES_DIGEST")}
	for i := range c.RepositoryIDs {
		c.RepositoryIDs[i] = strings.TrimSpace(c.RepositoryIDs[i])
	}
	for name, target := range map[string]*int64{"MAX_FILE_BYTES": &c.MaxFileBytes, "MAX_QUERY_BYTES": &c.MaxQueryBytes, "MAX_STATE_BYTES": &c.MaxStateBytes} {
		if raw := value(name); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return Config{}, ErrCapacity
			}
			*target = v
		}
	}
	for name, target := range map[string]*int{"MAX_FILES": &c.MaxFiles, "MAX_MEMBERS": &c.MaxMembers} {
		if raw := value(name); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				return Config{}, ErrCapacity
			}
			*target = v
		}
	}
	for name, target := range map[string]*time.Duration{"QUERY_TIMEOUT": &c.QueryTimeout, "RETIREMENT_GRACE": &c.RetirementGrace} {
		if raw := value(name); raw != "" {
			v, err := time.ParseDuration(raw)
			if err != nil {
				return Config{}, ErrCapacity
			}
			*target = v
		}
	}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if !accountPattern.MatchString(c.Account) || !containerPattern.MatchString(c.Container) ||
		strings.Contains(c.Container, "--") || !componentPattern.MatchString(c.Prefix) {
		return errors.New("archive requires an exact public-cloud Blob account, private container and single prefix")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		!strings.HasSuffix(u.Hostname(), ".kusto.windows.net") ||
		strings.HasPrefix(u.Hostname(), "ingest-") || !identifierPattern.MatchString(c.Database) ||
		!identifierPattern.MatchString(c.Table) {
		return errors.New("archive requires an exact HTTPS public-cloud ADX query endpoint and identifiers")
	}
	c.Endpoint = strings.TrimSuffix(c.Endpoint, "/")
	if !uuidPattern.MatchString(c.TenantID) || !uuidPattern.MatchString(c.ClientID) || strings.TrimSpace(c.ClientSecret) == "" {
		return errors.New("archive requires explicit Entra application credentials")
	}
	if c.Namespace == "" || len(c.Namespace) > 512 || !c.DataApproved || !c.SingleReplica {
		return errors.New("archive requires namespace, content/destination approval and single persistent owner")
	}
	if len(c.RepositoryIDs) == 0 || len(c.RepositoryIDs) > 1000 {
		return ErrScope
	}
	c.RepositoryIDs = slices.Clone(c.RepositoryIDs)
	slices.Sort(c.RepositoryIDs)
	for i, id := range c.RepositoryIDs {
		if id == "" || len(id) > 512 || strings.TrimSpace(id) != id || (i > 0 && id == c.RepositoryIDs[i-1]) {
			return ErrScope
		}
	}
	if c.MaxFileBytes == 0 {
		c.MaxFileBytes = 128 << 20
	}
	if c.MaxFiles == 0 {
		c.MaxFiles = 128
	}
	if c.MaxQueryBytes == 0 {
		c.MaxQueryBytes = 256 << 20
	}
	if c.MaxMembers == 0 {
		c.MaxMembers = 4096
	}
	if c.MaxStateBytes == 0 {
		c.MaxStateBytes = 4 << 20
	}
	if c.QueryTimeout == 0 {
		c.QueryTimeout = 30 * time.Second
	}
	if c.RetirementGrace == 0 {
		c.RetirementGrace = 24 * time.Hour
	}
	if c.MaxFileBytes < 1 || c.MaxFileBytes > 128<<20 || c.MaxQueryBytes < 1 || c.MaxQueryBytes > 1<<30 || c.MaxFiles < 1 || c.MaxFiles > 128 ||
		c.MaxMembers < 1 || c.MaxMembers > 4096 || c.MaxStateBytes < 1024 || c.MaxStateBytes > 4<<20 ||
		c.QueryTimeout < time.Second || c.QueryTimeout > 2*time.Minute ||
		c.RetirementGrace < 5*time.Minute || c.RetirementGrace > 30*24*time.Hour {
		return ErrCapacity
	}
	return nil
}

func (c Config) blobRoot() string {
	return "https://" + c.Account + ".blob.core.windows.net/" + c.Container + "/" + c.Prefix
}

// AcceptanceBinding binds operator acceptance to the precise destination,
// scope, plan and resource policy. It intentionally contains no credentials.
func (c Config) AcceptanceBinding() string {
	_ = c.Validate()
	data, _ := json.Marshal([]any{planVersion, c.Account, c.Container, c.Prefix,
		c.Endpoint, c.Database, c.Table, c.Namespace, c.RepositoryIDs,
		c.ExternalPropertiesDigest, c.MaxFileBytes, c.MaxQueryBytes, c.MaxFiles, c.MaxMembers, c.MaxStateBytes, c.QueryTimeout, c.RetirementGrace})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (c Config) authorized(namespace, repository string) bool {
	return namespace == c.Namespace && slices.Contains(c.RepositoryIDs, repository)
}
