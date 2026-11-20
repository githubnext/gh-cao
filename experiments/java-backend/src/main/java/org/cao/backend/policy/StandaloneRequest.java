package org.cao.backend.policy;

/**
 * Verified static identity plus narrowing-only dispatch requests for a standalone workflow
 * (specs/control-architecture.md CAO-EXE-005). Request fields are null when absent.
 *
 * @param controlRepository owner/repository running the workflow
 * @param workflow exact compiled workflow slug from static source
 * @param workflowRef {@code GITHUB_WORKFLOW_REF}; must name the workflow's lock file in the control repository
 * @param workflowSha exact 40-character lowercase workflow commit
 * @param targetRepository optional single requested target
 * @param safeOutputRepository requested review destination
 * @param correlationId dispatcher envelope field; must be absent
 * @param centralRepository dispatcher envelope field; must be absent
 * @param controlPlaneRunUrl dispatcher envelope field; must be absent
 */
public record StandaloneRequest(
        String controlRepository,
        String workflow,
        String workflowRef,
        String workflowSha,
        String targetRepository,
        String safeOutputMode,
        Integer maxRepositories,
        Integer rolloutPercent,
        String safeOutputRepository,
        String correlationId,
        String centralRepository,
        String controlPlaneRunUrl) { }
