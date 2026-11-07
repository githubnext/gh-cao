package org.cao.backend.service;

import java.util.List;
import java.util.Map;

/** Operational-state health as exposed under {@code operational} and the deprecated {@code redis} alias. */
public record OperationalStatus(boolean connected, boolean recoveryReady, Map<String, Capability> capabilities) {
    /** Capability names required by the OpenAPI OperationalCapabilities schema, in wire order. */
    public static final List<String> CAPABILITY_NAMES = List.of(
            "cache", "requestLimits", "sessions", "revocations",
            "githubQuota", "collection", "coordination", "diagnostics");

    public OperationalStatus {
        capabilities = Map.copyOf(capabilities);
        for (String name : CAPABILITY_NAMES) {
            if (!capabilities.containsKey(name)) {
                throw new IllegalArgumentException("missing capability " + name);
            }
        }
    }

    /** Status of a deployment with no operational dependency: nothing connected, nothing supported. */
    public static OperationalStatus unconfigured() {
        Map<String, Capability> all = new java.util.HashMap<>();
        CAPABILITY_NAMES.forEach(name -> all.put(name, Capability.UNSUPPORTED));
        return new OperationalStatus(false, false, all);
    }
}
