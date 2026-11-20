package org.cao.backend.policy;

import java.util.List;
import java.util.Map;

/** Outcome of standalone policy resolution; denials are expected outcomes, other failures throw. */
public record StandaloneResolution(
        boolean authorized,
        String reason,
        String workflow,
        Mode mode,
        int maxRepositories,
        int rolloutPercent,
        Map<String, Mode> targetModes,
        List<String> allowedOwners,
        List<String> allowedRepositories,
        ControlPolicy.Inventory inventory,
        String controlRepository,
        String policyDigest) {

    public static StandaloneResolution denied(String reason, StandaloneRequest request, String digest) {
        return new StandaloneResolution(false, reason, request.workflow(), Mode.REVIEW, 0, 0, Map.of(),
                List.of(), List.of(), ControlPolicy.Inventory.defaults(), request.controlRepository(), digest);
    }
}
