package server

import "testing"

func TestTargetPolicyOverridesCapabilities(t *testing.T) {
	trueValue := true
	tests := []struct {
		name   string
		policy targetPolicy
		want   bool
	}{
		{name: "no overrides", policy: targetPolicy{Module: "container"}, want: false},
		{name: "authentication override", policy: targetPolicy{Authentication: HostAuthenticationOAuth}, want: true},
		{name: "listener override", policy: targetPolicy{Listener: HostListenerPlatform}, want: true},
		{name: "require-https override", policy: targetPolicy{RequireHTTPS: &trueValue}, want: true},
		{name: "trust-platform-proxy override", policy: targetPolicy{TrustPlatformProxy: &trueValue}, want: true},
		{name: "supports-single-replica override", policy: targetPolicy{SupportsSingleReplica: &trueValue}, want: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := targetPolicyOverridesCapabilities(testCase.policy); got != testCase.want {
				t.Errorf("targetPolicyOverridesCapabilities(%+v) = %t, want %t", testCase.policy, got, testCase.want)
			}
		})
	}
}

func TestApplyTargetModuleOverridesRejectsOverridesOnFixedModule(t *testing.T) {
	fixed := hostTargetModules["container"]
	if _, err := applyTargetModuleOverrides(fixed, targetPolicy{Module: "container", Listener: HostListenerPlatform}); err == nil {
		t.Fatal("expected an error when overriding a fixed host target module's capabilities")
	}
}

func TestApplyTargetModuleOverridesAcceptsFixedModuleWithoutOverrides(t *testing.T) {
	fixed := hostTargetModules["container"]
	resolved, err := applyTargetModuleOverrides(fixed, targetPolicy{Module: "container"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != fixed {
		t.Errorf("applyTargetModuleOverrides changed a fixed module without any override: got %+v, want %+v", resolved, fixed)
	}
}

func TestApplyTargetModuleOverridesRequiresAuthenticationAndListenerOnGeneric(t *testing.T) {
	generic := hostTargetModules["generic"]
	if _, err := applyTargetModuleOverrides(generic, targetPolicy{Module: "generic"}); err == nil {
		t.Fatal("expected an error when the generic module is missing authentication and listener")
	}
	if _, err := applyTargetModuleOverrides(generic, targetPolicy{
		Module:   "generic",
		Listener: HostListenerProcess,
	}); err == nil {
		t.Fatal("expected an error when the generic module is missing authentication")
	}
}

func TestApplyTargetModuleOverridesRejectsNonOAuthAuthenticationOnGeneric(t *testing.T) {
	generic := hostTargetModules["generic"]
	if _, err := applyTargetModuleOverrides(generic, targetPolicy{
		Module:         "generic",
		Authentication: HostAuthenticationBearer,
		Listener:       HostListenerProcess,
	}); err == nil {
		t.Fatal("expected an error when the generic module requests bearer authentication")
	}
}

func TestApplyTargetModuleOverridesMergesGenericCapabilities(t *testing.T) {
	generic := hostTargetModules["generic"]
	requireHTTPS := true
	trustProxy := true
	singleReplica := true
	resolved, err := applyTargetModuleOverrides(generic, targetPolicy{
		Module:                "generic",
		Authentication:        HostAuthenticationOAuth,
		Listener:              HostListenerPlatform,
		RequireHTTPS:          &requireHTTPS,
		TrustPlatformProxy:    &trustProxy,
		SupportsSingleReplica: &singleReplica,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.authentication != HostAuthenticationOAuth ||
		resolved.listener != HostListenerPlatform ||
		!resolved.requireHTTPS ||
		!resolved.trustPlatformProxy ||
		!resolved.supportsSingleReplica {
		t.Fatalf("applyTargetModuleOverrides did not merge overrides: %+v", resolved)
	}
}

func TestGenericHostCannotDisableHTTPS(t *testing.T) {
	disabled := false
	for _, listener := range []HostListener{HostListenerProcess, HostListenerExternal, HostListenerPlatform} {
		if _, err := applyTargetModuleOverrides(hostTargetModules["generic"], targetPolicy{
			Module: "generic", Authentication: HostAuthenticationOAuth,
			Listener: listener, RequireHTTPS: &disabled,
		}); err == nil {
			t.Fatalf("generic %s listener accepted disabled HTTPS", listener)
		}
	}
}

func TestApplyTargetModuleOverridesDefaultsRequireHTTPSToTrueOnGeneric(t *testing.T) {
	generic := hostTargetModules["generic"]
	resolved, err := applyTargetModuleOverrides(generic, targetPolicy{
		Module:         "generic",
		Authentication: HostAuthenticationOAuth,
		Listener:       HostListenerProcess,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resolved.requireHTTPS {
		t.Error("expected the generic module to default require-https to true without an explicit override")
	}
}
