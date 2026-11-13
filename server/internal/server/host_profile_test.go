package server

import (
	"strings"
	"testing"
)

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
		collection bool
	}{
		{localHostProfile(), HostAuthenticationBearer, HostListenerProcess, true},
		{hostedHostProfile(), HostAuthenticationOAuth, HostListenerProcess, true},
		{platformHostProfile(), HostAuthenticationOAuth, HostListenerPlatform, true},
		{func() HostProfile { p := hostedHostProfile(); p.Listener = HostListenerExternal; return p }(), HostAuthenticationOAuth, HostListenerExternal, true},
	}
	for _, test := range tests {
		t.Run(test.profile.Name, func(t *testing.T) {
			if err := test.profile.validate(); err != nil {
				t.Fatal(err)
			}
			if test.profile.Authentication != test.auth ||
				test.profile.Listener != test.listener ||
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
			name:            "unsupported collection",
			profile:         func() HostProfile { p := valid; p.SupportsCollection = false; return p }(),
			config:          Config{Collector: &CollectorConfig{}},
			storeConfigured: true,
			want:            hostProfileRejectionReasonUnsupportedCollector,
		},
		{
			name:            "unconfirmed single replica",
			profile:         func() HostProfile { p := valid; p.SingleReplica = true; return p }(),
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

func TestLocalHostProfileRejectsOAuthConfiguration(t *testing.T) {
	config := Config{
		HostProfile: localHostProfile(),
		GitHubOAuth: &GitHubOAuthConfig{},
	}
	if err := validateHostProfile(newTestOperationalStore(t), &config); err == nil ||
		!strings.Contains(err.Error(), "does not support GitHub OAuth") {
		t.Fatalf("local profile accepted OAuth: %v", err)
	}
}

func TestPlatformHostProfileRejectsProcessListenerAndEnforcesTrustedProxy(t *testing.T) {

	store := newTestOperationalStore(t)
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
