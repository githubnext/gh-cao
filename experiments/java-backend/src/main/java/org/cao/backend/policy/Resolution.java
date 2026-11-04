package org.cao.backend.policy;

/**
 * Outcome of policy resolution. A denial is an expected outcome (native noop, no model
 * invocation); fatal errors are thrown as {@link PolicyException} instead.
 */
public record Resolution(
        boolean authorized,
        String reason,
        Role role,
        String campaign,
        String worker,
        String targetRepository,
        Mode mode,
        int maxRepositories,
        int rolloutPercent,
        String controlRepository,
        String policyDigest) {

    public static Resolution denied(String reason, ResolutionRequest request, String digest) {
        return new Resolution(false, reason, request.role(), request.campaign(), request.worker(),
                request.targetRepository(), Mode.REVIEW, 0, 0, request.controlRepository(), digest);
    }
}
