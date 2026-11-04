package org.cao.backend.policy;

/**
 * Parent authority carried by a dispatch. It is provenance and an upper bound only; a worker never
 * treats it as current authority (specs/control-architecture.md §6.4).
 */
public record DispatchEnvelope(
        String controlRepository,
        String campaign,
        String worker,
        String targetRepository,
        Mode mode,
        int maxRepositories,
        int rolloutPercent,
        String policyDigest) {

    public static DispatchEnvelope from(Resolution parent) {
        if (!parent.authorized() || parent.worker() == null || parent.targetRepository() == null) {
            throw new PolicyException("an envelope can only be derived from an authorized worker resolution");
        }
        return new DispatchEnvelope(parent.controlRepository(), parent.campaign(), parent.worker(),
                parent.targetRepository(), parent.mode(), parent.maxRepositories(), parent.rolloutPercent(),
                parent.policyDigest());
    }
}
