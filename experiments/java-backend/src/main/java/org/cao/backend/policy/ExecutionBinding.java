package org.cao.backend.policy;

import java.util.regex.Pattern;

/**
 * Verified execution context an authority envelope is bound to (CAO-EXE-006). The caller asserts the
 * values were verified; this record only validates their shape, it does not verify provenance.
 */
public record ExecutionBinding(String repository, String policySha, String workflowRef, String runId,
        String runAttempt) {
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final Pattern SHA = Pattern.compile("^[a-fA-F0-9]{40,64}$");
    private static final Pattern COUNTER = Pattern.compile("^[1-9][0-9]*$");

    public ExecutionBinding {
        if (repository == null || !REPOSITORY.matcher(repository).matches()
                || policySha == null || !SHA.matcher(policySha).matches()
                || workflowRef == null || !workflowRef.startsWith(repository + "/.github/workflows/")
                || runId == null || !COUNTER.matcher(runId).matches()
                || runAttempt == null || !COUNTER.matcher(runAttempt).matches()) {
            throw new PolicyException("envelope requires verified execution identity and policy revision");
        }
    }

    /** The workflow reference must be exactly {@code <repo>/.github/workflows/<slug>.lock.yml@<ref>}. */
    void requireWorkflowIdentity(String slug, String role) {
        String prefix = repository + "/.github/workflows/" + slug + ".lock.yml@";
        if (!workflowRef.startsWith(prefix) || workflowRef.length() == prefix.length()) {
            throw new PolicyException(role + " identity must match GITHUB_WORKFLOW_REF");
        }
    }
}
