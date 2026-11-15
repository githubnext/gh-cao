package org.cao.backend.policy;

/**
 * Static identity plus narrowing-only requests for a standalone workflow run. Campaign, worker and
 * dispatcher fields exist only so that their presence can be rejected (CAO-EXE-005).
 *
 * @param workflowRef verified {@code GITHUB_WORKFLOW_REF}
 * @param workflow exact compiled workflow slug
 * @param targetRepository optional single-target shortcut
 * @param safeOutputMode requested mode text ("debug" is rejected)
 * @param safeOutputRepository requested review destination
 * @param destinationPrivate evidence that a non-central review destination is private; null when unavailable
 */
public record StandaloneRequest(
        String controlRepository,
        String workflowRef,
        String workflow,
        String campaign,
        String worker,
        String correlationId,
        String centralRepository,
        String controlPlaneRunUrl,
        String targetRepository,
        String safeOutputMode,
        String safeOutputRepository,
        Boolean destinationPrivate,
        Integer maxRepositories,
        Integer rolloutPercent) { }
