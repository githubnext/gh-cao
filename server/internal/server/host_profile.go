package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

type HostAuthentication string

const (
	HostAuthenticationBearer HostAuthentication = "bearer"
	HostAuthenticationOAuth  HostAuthentication = "github-oauth"
)

type HostListener string

const (
	HostListenerProcess  HostListener = "process"
	HostListenerPlatform HostListener = "platform"
	HostListenerExternal HostListener = "external"
)

type HostRedisSession string

const (
	HostRedisPooled     HostRedisSession = "pooled"
	HostRedisSerialized HostRedisSession = "serialized"
)

// HostProfile describes runtime capabilities independently of a deployment
// provider. Platform integrations select a profile and supply its configuration.
type HostProfile struct {
	Name                    string
	Authentication          HostAuthentication
	Listener                HostListener
	RequiresHTTPS           bool
	TrustsPlatformProxy     bool
	RequiresRedis           bool
	RedisSession            HostRedisSession
	IsolateProcessNamespace bool
	SingleReplica           bool
	SingleProcess           bool
	SupportsCollection      bool
}

func localHostProfile() HostProfile {
	return HostProfile{
		Name:               "local",
		Authentication:     HostAuthenticationBearer,
		Listener:           HostListenerProcess,
		RequiresRedis:      false,
		RedisSession:       HostRedisPooled,
		SupportsCollection: true,
	}
}

func hostedHostProfile() HostProfile {
	return HostProfile{
		Name:               "hosted",
		Authentication:     HostAuthenticationOAuth,
		Listener:           HostListenerProcess,
		RequiresHTTPS:      true,
		RequiresRedis:      false,
		RedisSession:       HostRedisPooled,
		SupportsCollection: true,
	}
}

func azureLocalSimulationProfile(profile HostProfile, localSimulation bool) HostProfile {
	if localSimulation {
		profile.RequiresHTTPS = false
	}
	return profile
}

func (profile HostProfile) validate() error {
	if strings.TrimSpace(profile.Name) == "" {
		return errors.New("host profile requires a name")
	}
	if profile.Authentication != HostAuthenticationBearer &&
		profile.Authentication != HostAuthenticationOAuth {
		return fmt.Errorf("host profile %q has unsupported authentication %q", profile.Name, profile.Authentication)
	}
	if profile.Listener != HostListenerProcess &&
		profile.Listener != HostListenerPlatform &&
		profile.Listener != HostListenerExternal {
		return fmt.Errorf("host profile %q has unsupported listener %q", profile.Name, profile.Listener)
	}
	if profile.Listener != HostListenerProcess && profile.Authentication != HostAuthenticationOAuth {
		return fmt.Errorf("host profile %q cannot use bearer authentication with an externally owned listener", profile.Name)
	}
	if profile.Authentication == HostAuthenticationOAuth &&
		profile.Listener != HostListenerPlatform && !profile.RequiresHTTPS {
		return fmt.Errorf("host profile %q requires HTTPS for hosted authentication", profile.Name)
	}
	if profile.TrustsPlatformProxy && profile.Listener != HostListenerPlatform {
		return fmt.Errorf("host profile %q trusts a platform proxy without a platform listener", profile.Name)
	}
	if profile.SingleProcess && (!profile.SingleReplica ||
		profile.Authentication != HostAuthenticationOAuth || !profile.RequiresHTTPS ||
		profile.Listener != HostListenerProcess) {
		return fmt.Errorf("host profile %q requires OAuth HTTPS and one owning process listener", profile.Name)
	}
	return nil
}

// hostProfileRejectionReason classifies which deployment precondition
// validateHostProfile rejected. It is useful for diagnosing a misconfigured
// deployment without logging the host profile's name or the Redis, listener,
// or OAuth configuration values that produced the rejection.
type hostProfileRejectionReason string

const (
	hostProfileRejectionReasonNone                 hostProfileRejectionReason = "none"
	hostProfileRejectionReasonInvalidProfile       hostProfileRejectionReason = "invalid-profile"
	hostProfileRejectionReasonMissingStore         hostProfileRejectionReason = "missing-operational-store"
	hostProfileRejectionReasonUnsupportedCollector hostProfileRejectionReason = "unsupported-collection"
	hostProfileRejectionReasonUnconfirmedReplica   hostProfileRejectionReason = "unconfirmed-single-replica"
	hostProfileRejectionReasonUnsupportedOAuth     hostProfileRejectionReason = "unsupported-oauth"
	hostProfileRejectionReasonDelegatedListener    hostProfileRejectionReason = "delegated-listener-conflict"
)

// classifyHostProfileRejection checks authentication, listener and topology
// requirements independently of provider connection details.
func classifyHostProfileRejection(
	profile HostProfile, config Config, storeConfigured bool,
) hostProfileRejectionReason {
	if err := profile.validate(); err != nil {
		return hostProfileRejectionReasonInvalidProfile
	}
	if !storeConfigured {
		return hostProfileRejectionReasonMissingStore
	}
	if !profile.SupportsCollection && config.Collector != nil {
		return hostProfileRejectionReasonUnsupportedCollector
	}
	if profile.SingleReplica && !config.SingleReplicaConfirmed {
		return hostProfileRejectionReasonUnconfirmedReplica
	}
	if profile.Authentication == HostAuthenticationBearer && config.GitHubOAuth != nil {
		return hostProfileRejectionReasonUnsupportedOAuth
	}
	if profile.Listener != HostListenerProcess &&
		(strings.TrimSpace(config.Listen) != "" || config.CertFile != "" || config.KeyFile != "") {
		return hostProfileRejectionReasonDelegatedListener
	}
	return hostProfileRejectionReasonNone
}

func validateHostProfile(store operational.Store, config *Config) error {
	profile := config.HostProfile
	if profile == (HostProfile{}) {
		profile = localHostProfile()
		config.HostProfile = profile
	}
	// Provider session/isolation checks belong to adapter composition, not
	// host authentication and listener validation.
	reason := classifyHostProfileRejection(profile, *config, store != nil)
	serverLog.Printf("host profile validated profile=%s reason=%s", profile.Name, reason)
	switch reason {
	case hostProfileRejectionReasonNone:
		// fall through to apply the profile's implied configuration below.
	case hostProfileRejectionReasonInvalidProfile:
		return profile.validate()
	case hostProfileRejectionReasonMissingStore:
		return fmt.Errorf("host profile %q requires operational storage", profile.Name)
	case hostProfileRejectionReasonUnsupportedCollector:
		return fmt.Errorf("host profile %q does not support server-side collection", profile.Name)
	case hostProfileRejectionReasonUnconfirmedReplica:
		return fmt.Errorf("host profile %q requires a confirmed single-replica deployment", profile.Name)
	case hostProfileRejectionReasonUnsupportedOAuth:
		return fmt.Errorf("host profile %q does not support GitHub OAuth", profile.Name)
	case hostProfileRejectionReasonDelegatedListener:
		return fmt.Errorf("host profile %q must not configure a process listener or TLS files", profile.Name)
	}
	if profile.TrustsPlatformProxy {
		config.Proxy.TrustForwarded = true
	}
	capabilities := store.Capabilities()
	if capabilities.Collection.Scope == operational.ScopeProcess &&
		capabilities.Collection.Persistence == operational.PersistenceVolatile &&
		!profile.SingleProcess {
		return errors.New("process-local volatile state requires an explicit single-process host profile, not only replica confirmation")
	}
	if profile.SingleProcess {
		if profile.Authentication != HostAuthenticationOAuth || config.GitHubOAuth == nil ||
			!profile.RequiresHTTPS || !config.Proxy.RequireHTTPS || profile.Listener != HostListenerProcess {
			return errors.New("single-process volatile hosting requires GitHub OAuth and HTTPS")
		}
		if !config.AllowVolatile {
			return errors.New("single-process volatile hosting requires allow-volatile acknowledgement")
		}
		if config.Collector != nil && config.Collector.AdmitOnly {
			return errors.New("single-process volatile hosting cannot use admission-only collection")
		}
	}
	return nil
}
