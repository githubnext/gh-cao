package org.cao.backend.policy;

import java.util.List;
import java.util.Map;

/**
 * Evidence an orchestrator resolver needs beyond policy. The values are supplied by a caller that has
 * already acquired them under job-local credentials; this module never contacts GitHub.
 *
 * @param source where the inventory came from
 * @param repositories complete bounded inventory, or {@code null} when it could not be read (fatal)
 * @param configuredWorkflows worker workflow slugs declared by the compiled orchestrator, in order
 * @param installedWorkflows installed workflow state keyed by workflow slug
 * @param dispatchMax per-run dispatch ceiling, 1..1000
 * @param safeOutputRepository review or live routing repository for the run
 * @param safeOutputRepositoryPrivate whether a non-control review repository is private; {@code null} if unknown
 */
public record OrchestratorEvidence(
        Source source,
        List<RepositoryMetadata> repositories,
        List<String> configuredWorkflows,
        Map<String, WorkflowState> installedWorkflows,
        int dispatchMax,
        String safeOutputRepository,
        Boolean safeOutputRepositoryPrivate) {

    /** Inventory origin: an exact allow-list is read repository by repository, otherwise the owner is listed. */
    public enum Source {
        ORGANIZATION("organization"),
        ALLOWED_REPOSITORIES("allowed_repos");

        private final String wire;

        Source(String wire) {
            this.wire = wire;
        }

        public String wire() {
            return wire;
        }
    }

    /** Installed workflow identity; {@code active} is false for disabled workflows. */
    public record WorkflowState(long id, String name, String path, String state, boolean active) { }
}
