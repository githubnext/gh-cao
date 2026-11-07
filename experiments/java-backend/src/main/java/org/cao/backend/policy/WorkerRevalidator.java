package org.cao.backend.policy;

import java.util.Locale;

/**
 * Worker revalidation (specs/control-architecture.md §6.4, T-RES-004/005): current policy is resolved
 * independently and intersected with the parent envelope. Newer policy may narrow or revoke an
 * outstanding dispatch, never widen it.
 */
public final class WorkerRevalidator {
    private WorkerRevalidator() { }

    public static Resolution revalidate(ControlPolicy current, ResolutionRequest request, DispatchEnvelope envelope) {
        if (current == null || request == null || envelope == null) {
            throw new PolicyException("policy, request and parent envelope are required");
        }
        if (request.role() != Role.WORKER) {
            throw new PolicyException("only workers revalidate against a parent envelope");
        }
        if (!envelope.controlRepository().equalsIgnoreCase(request.controlRepository())
                || !envelope.campaign().equals(request.campaign())
                || !envelope.worker().equals(request.worker())
                || !envelope.targetRepository().toLowerCase(Locale.ROOT)
                        .equals(String.valueOf(request.targetRepository()).toLowerCase(Locale.ROOT))) {
            throw new PolicyException("dispatch envelope does not match the static worker identity or target");
        }
        Resolution now = PolicyResolver.resolve(current, request);
        if (!now.authorized()) {
            return now;
        }
        return new Resolution(true, "authorized", now.role(), now.campaign(), now.worker(),
                now.targetRepository(), now.mode().lesser(envelope.mode()),
                Math.min(now.maxRepositories(), envelope.maxRepositories()),
                Math.min(now.rolloutPercent(), envelope.rolloutPercent()),
                now.controlRepository(), now.policyDigest());
    }
}
