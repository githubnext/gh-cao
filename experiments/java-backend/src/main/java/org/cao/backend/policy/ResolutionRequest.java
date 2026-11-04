package org.cao.backend.policy;

/**
 * Static identity plus dispatch requests. Request fields are narrowing-only and null when absent.
 *
 * @param controlRepository owner/repository running the workflow
 * @param campaign static campaign slug
 * @param worker static worker slug; required for {@link Role#WORKER}
 * @param targetRepository requested single target; required for workers
 * @param safeOutputMode requested mode string exactly as supplied ("debug" is rejected)
 * @param maxRepositories requested repository ceiling
 * @param rolloutPercent requested rollout ceiling
 */
public record ResolutionRequest(
        Role role,
        String controlRepository,
        String campaign,
        String worker,
        String targetRepository,
        String safeOutputMode,
        Integer maxRepositories,
        Integer rolloutPercent) { }
