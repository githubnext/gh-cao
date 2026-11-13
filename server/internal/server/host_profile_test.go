package server

import (
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func upstashHostProfile() HostProfile {
	profile := hostedHostProfile()
	profile.Name = "upstash"
	profile.RedisSession = HostRedisSerialized
	profile.IsolateProcessNamespace = true
	profile.SingleReplica = true
	profile.SupportsCollection = false
	return profile
}

func platformHostProfile() HostProfile {
	profile := hostedHostProfile()
	profile.Name = "platform"
	profile.Listener = HostListenerPlatform
	profile.TrustsPlatformProxy = true
	return profile
}

func TestBuiltInHostProfilesDeclareExpectedCapabilities(t *testing.T) {
	tests := []struct {
		profile    HostProfile
		auth       HostAuthentication
		listener   HostListener
		session    HostRedisSession
		collection bool
	}{
		{localHostProfile(), HostAuthenticationBearer, HostListenerProcess, HostRedisPooled, true},
		{hostedHostProfile(), HostAuthenticationOAuth, HostListenerProcess, HostRedisPooled, true},
		{platformHostProfile(), HostAuthenticationOAuth, HostListenerPlatform, HostRedisPooled, true},
		{func() HostProfile { p := hostedHostProfile(); p.Listener = HostListenerExternal; return p }(), HostAuthenticationOAuth, HostListenerExternal, HostRedisPooled, true},
		{upstashHostProfile(), HostAuthenticationOAuth, HostListenerProcess, HostRedisSerialized, false},
	}
	for _, test := range tests {
		t.Run(test.profile.Name, func(t *testing.T) {
			if err := test.profile.validate(); err != nil {
				t.Fatal(err)
			}
			if test.profile.Authentication != test.auth ||
				test.profile.Listener != test.listener ||
				test.profile.RedisSession != test.session ||
				test.profile.SupportsCollection != test.collection {
				t.Fatalf("unexpected capabilities: %+v", test.profile)
			}
		})
	}
}

func TestHostProfileRejectsInconsistentCapabilities(t *testing.T) {
	base := hostedHostProfile()
	tests := []HostProfile{
		func() HostProfile {
			value := base
			value.Authentication = ""
			return value
		}(),
		func() HostProfile {
			value := base
			value.Listener = ""
			return value
		}(),
		func() HostProfile {
			value := base
			value.Authentication = HostAuthenticationBearer
			value.Listener = HostListenerPlatform
			return value
		}(),
		func() HostProfile {
			value := base
			value.Authentication = HostAuthenticationBearer
			value.Listener = HostListenerExternal
			return value
		}(),
		func() HostProfile {
			value := base
			value.Listener = HostListenerExternal
			value.RequiresHTTPS = false
			return value
		}(),
		func() HostProfile {
			value := base
			value.Listener = HostListenerExternal
			value.TrustsPlatformProxy = true
			return value
		}(),
	}

	for _, profile := range tests {
		if err := profile.validate(); err == nil {
			t.Fatalf("accepted inconsistent profile: %+v", profile)
		}
	}
}

func TestHostProfileDefaultsOnlyWhenCompletelyUnset(t *testing.T) {
	config := Config{}
	if err := validateHostProfile(nil, &config); err == nil ||
		!strings.Contains(err.Error(), "requires operational storage") {
		t.Fatalf("default local profile did not require operational storage: %v", err)
	}

	config.HostProfile = HostProfile{Authentication: HostAuthenticationBearer}
	if err := validateHostProfile(nil, &config); err == nil ||
		!strings.Contains(err.Error(), "requires a name") {
		t.Fatalf("partially configured profile silently defaulted: %v", err)
	}
}

func TestClassifyRedisSessionMismatchDetectsIncompatibleSessions(t *testing.T) {
	tests := []struct {
		name           string
		required       HostRedisSession
		reportsSession bool
		singleSession  bool
		mismatch       bool
	}{
		{"serialized requires reported single session", HostRedisSerialized, true, true, false},
		{"serialized rejects unreported session", HostRedisSerialized, false, false, true},
		{"serialized rejects reported non-single session", HostRedisSerialized, true, false, true},
		{"pooled requires non-single session", HostRedisPooled, false, false, false},
		{"pooled rejects reported single session", HostRedisPooled, true, true, true},
		{"pooled accepts reported non-single session", HostRedisPooled, true, false, false},
		{"unknown session kind never mismatches", HostRedisSession("unknown"), true, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyRedisSessionMismatch(test.required, test.reportsSession, test.singleSession)
			if got != test.mismatch {
				t.Fatalf("classifyRedisSessionMismatch(%q, %t, %t) = %t, want %t",
					test.required, test.reportsSession, test.singleSession, got, test.mismatch)
			}
		})
	}
}

func TestClassifyHostProfileRejectionOrdersChecksAndReportsReason(t *testing.T) {
	valid := hostedHostProfile()

	tests := []struct {
		name            string
		profile         HostProfile
		config          Config
		storeConfigured bool
		want            hostProfileRejectionReason
	}{
		{
			name:    "invalid profile rejected before any config check",
			profile: HostProfile{},
			want:    hostProfileRejectionReasonInvalidProfile,
		},
		{
			name:    "missing operational storage",
			profile: valid,
			want:    hostProfileRejectionReasonMissingStore,
		},
		{
			name:            "Redis session checks remain in composition",
			profile:         valid,
			storeConfigured: true,
			want:            hostProfileRejectionReasonNone,
		},
		{
			name: "Redis namespace checks remain in composition",
			profile: func() HostProfile {
				p := valid
				p.RedisSession = HostRedisSerialized
				p.IsolateProcessNamespace = true
				p.SingleReplica = true
				return p
			}(),
			storeConfigured: true,
			config:          Config{SingleReplicaConfirmed: true},
			want:            hostProfileRejectionReasonNone,
		},
		{
			name:            "unsupported collection",
			profile:         func() HostProfile { p := valid; p.SupportsCollection = false; return p }(),
			config:          Config{Collector: &CollectorConfig{}},
			storeConfigured: true,
			want:            hostProfileRejectionReasonUnsupportedCollector,
		},
		{
			name:            "unconfirmed single replica",
			profile:         func() HostProfile { p := valid; p.RedisSession = HostRedisSerialized; p.SingleReplica = true; return p }(),
			storeConfigured: true,
			want:            hostProfileRejectionReasonUnconfirmedReplica,
		},
		{
			name:            "unsupported oauth",
			profile:         localHostProfile(),
			config:          Config{GitHubOAuth: &GitHubOAuthConfig{}},
			storeConfigured: true,
			want:            hostProfileRejectionReasonUnsupportedOAuth,
		},
		{
			name:            "platform listener conflict",
			profile:         platformHostProfile(),
			config:          Config{Listen: "127.0.0.1:8080"},
			storeConfigured: true,
			want:            hostProfileRejectionReasonDelegatedListener,
		},
		{
			name:            "external listener rejects process configuration",
			profile:         func() HostProfile { p := valid; p.Listener = HostListenerExternal; return p }(),
			config:          Config{CertFile: "server.pem"},
			storeConfigured: true,
			want:            hostProfileRejectionReasonDelegatedListener,
		},
		{
			name:            "accepted configuration reports none",
			profile:         valid,
			storeConfigured: true,
			want:            hostProfileRejectionReasonNone,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyHostProfileRejection(
				test.profile, test.config, test.storeConfigured,
			)
			if got != test.want {
				t.Fatalf("classifyHostProfileRejection() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUpstashHostProfileRequiresSerializedClientAndArtifactIngestion(t *testing.T) {
	pooled, err := redisx.New("redis://127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{HostProfile: upstashHostProfile()}
	if err := validateHostProfile(nil, &config); err == nil ||
		!strings.Contains(err.Error(), "requires operational storage") {
		t.Fatalf("accepted missing Redis: %v", err)
	}
	config.SingleReplicaConfirmed = true
	if err := validateRedisStoreComposition(redisx.NewStore(pooled, "test"), config.HostProfile); err == nil ||
		!strings.Contains(err.Error(), "serialized Redis client") {
		t.Fatalf("accepted pooled client: %v", err)
	}

	serialized, err := redisx.NewWithOptions(
		"redis://127.0.0.1:6379",
		redisx.Options{SingleSession: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	standard := Config{HostProfile: hostedHostProfile()}
	if err := validateRedisStoreComposition(redisx.NewStore(serialized, "test"), standard.HostProfile); err == nil ||
		!strings.Contains(err.Error(), "pooled Redis client") {
		t.Fatalf("standard profile accepted serialized client: %v", err)
	}
	if err := validateRedisStoreComposition(redisx.NewStore(serialized, "test"), config.HostProfile); err == nil ||
		!strings.Contains(err.Error(), "namespace isolation") {
		t.Fatalf("accepted non-isolated store: %v", err)
	}
	store, err := redisx.NewProcessIsolatedStore(serialized, "test")
	if err != nil {
		t.Fatal(err)
	}
	config.SingleReplicaConfirmed = false
	if err := validateHostProfile(store, &config); err == nil ||
		!strings.Contains(err.Error(), "confirmed single-replica") {
		t.Fatalf("accepted unconfirmed replica count: %v", err)
	}
	config.SingleReplicaConfirmed = true
	config.Collector = &CollectorConfig{}
	if err := validateHostProfile(store, &config); err == nil ||
		!strings.Contains(err.Error(), "does not support server-side collection") {
		t.Fatalf("accepted collection profile: %v", err)
	}
	config.Collector = nil
	if err := validateHostProfile(store, &config); err != nil {
		t.Fatalf("rejected matching Upstash capabilities: %v", err)
	}
}

func TestLocalHostProfileRejectsOAuthConfiguration(t *testing.T) {
	client, err := redisx.New("redis://127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		HostProfile: localHostProfile(),
		GitHubOAuth: &GitHubOAuthConfig{},
	}
	if err := validateHostProfile(redisx.NewStore(client, "test"), &config); err == nil ||
		!strings.Contains(err.Error(), "does not support GitHub OAuth") {
		t.Fatalf("local profile accepted OAuth: %v", err)
	}
}

func TestPlatformHostProfileRejectsProcessListenerAndEnforcesTrustedProxy(t *testing.T) {
	client, err := redisx.New("redis://127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}

	store := redisx.NewStore(client, "test")
	config := Config{
		HostProfile: platformHostProfile(),
		Listen:      "127.0.0.1:8080",
		Proxy:       ProxyPolicy{TrustForwarded: true},
	}
	if err := validateHostProfile(store, &config); err == nil ||
		!strings.Contains(err.Error(), "must not configure a process listener") {
		t.Fatalf("accepted process listener: %v", err)
	}
	config.Listen = ""
	config.Proxy.TrustForwarded = false
	if err := validateHostProfile(store, &config); err != nil {
		t.Fatal(err)
	}
	if !config.Proxy.TrustForwarded {
		t.Fatal("platform proxy capability was not enforced")
	}
}

func TestPlatformHostProfileCannotServeProcessListener(t *testing.T) {
	app := &App{config: Config{HostProfile: platformHostProfile()}}
	if err := app.Serve(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "delegates listener ownership") {
		t.Fatalf("platform profile opened a process listener: %v", err)
	}
	profile := hostedHostProfile()
	profile.Listener = HostListenerExternal
	app.config.HostProfile = profile
	if err := app.Serve(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "delegates listener ownership") {
		t.Fatalf("external profile opened a process listener: %v", err)
	}
}
