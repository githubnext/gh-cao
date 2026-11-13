package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

// hostModulesLog gives observability into host target module resolution
// decisions made while applying the reviewed cao.json host policy. Only the
// module name and configurable/rejection outcome are logged: the policy
// itself may carry listener addresses and other deployment-specific values
// that must not reach a diagnostic log.
var hostModulesLog = logger.New("cao:server:host-modules")

type hostTargetModule struct {
	authentication        HostAuthentication
	listener              HostListener
	requireHTTPS          bool
	trustPlatformProxy    bool
	supportsSingleReplica bool
	configurable          bool
}

var hostTargetModules = map[string]hostTargetModule{
	"generic":   {configurable: true},
	"container": {authentication: HostAuthenticationOAuth, listener: HostListenerProcess, requireHTTPS: true, supportsSingleReplica: true},
}

// targetPolicyOverridesCapabilities reports whether policy supplies any of
// the target capability fields that only a configurable ("generic") host
// target module accepts. It is the single place both branches of
// applyTargetModuleOverrides consult, so a fixed module's rejection check
// can never drift from the configurable module's own override list.
func targetPolicyOverridesCapabilities(policy targetPolicy) bool {
	return policy.Authentication != "" || policy.Listener != "" ||
		policy.RequireHTTPS != nil || policy.TrustPlatformProxy != nil ||
		policy.SupportsSingleReplica != nil
}

// applyTargetModuleOverrides merges a targetPolicy's capability overrides
// into a copy of a host target module registry entry. It is a pure function,
// so the merge and validation rules governing a configurable module's
// overrides, and a fixed module's rejection of any override, are testable
// without constructing a resolvedHostPolicy.
func applyTargetModuleOverrides(module hostTargetModule, policy targetPolicy) (hostTargetModule, error) {
	if !module.configurable {
		if targetPolicyOverridesCapabilities(policy) {
			return hostTargetModule{}, fmt.Errorf("host target module %q has fixed capabilities", strings.TrimSpace(policy.Module))
		}
		return module, nil
	}
	if policy.Authentication == "" || policy.Listener == "" {
		return hostTargetModule{}, errors.New("generic host target module requires authentication and listener")
	}
	module.authentication = policy.Authentication
	module.listener = policy.Listener
	if module.authentication != HostAuthenticationOAuth {
		return hostTargetModule{}, errors.New("configured host target module requires github-oauth authentication")
	}
	module.requireHTTPS = true
	if policy.RequireHTTPS != nil {
		if !*policy.RequireHTTPS {
			return hostTargetModule{}, errors.New("configured host target module cannot disable HTTPS")
		}
	}
	if policy.TrustPlatformProxy != nil {
		module.trustPlatformProxy = *policy.TrustPlatformProxy
	}
	if policy.SupportsSingleReplica != nil {
		module.supportsSingleReplica = *policy.SupportsSingleReplica
	}
	return module, nil
}

func resolveTargetModule(policy targetPolicy) (HostProfile, bool, error) {
	moduleName := strings.TrimSpace(policy.Module)
	module, ok := hostTargetModules[moduleName]
	if !ok {
		return HostProfile{}, false, fmt.Errorf("unsupported host target module %q", moduleName)
	}
	configurable := module.configurable
	module, err := applyTargetModuleOverrides(module, policy)
	if err != nil {
		hostModulesLog.Printf("target module resolution rejected module=%q configurable=%t", moduleName, configurable)
		return HostProfile{}, false, err
	}
	name := strings.TrimSpace(policy.Name)
	if name == "" {
		name = moduleName
	}
	return HostProfile{
		Name:                name,
		Authentication:      module.authentication,
		Listener:            module.listener,
		RequiresHTTPS:       module.requireHTTPS,
		TrustsPlatformProxy: module.trustPlatformProxy,
	}, module.supportsSingleReplica, nil
}
