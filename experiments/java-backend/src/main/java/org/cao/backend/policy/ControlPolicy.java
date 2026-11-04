package org.cao.backend.policy;

import java.util.List;
import java.util.Map;

/** Validated subset of {@code .github/workflows/cao.json} control-plane policy (version 1). */
public record ControlPolicy(
        String digest,
        Scope scope,
        Defaults defaults,
        Map<String, Campaign> campaigns) {

    /** Owner and repository allow-lists; empty means "not restricted by this list". */
    public record Scope(List<String> owners, List<String> repositories) { }

    /** Nullable members mean the declaration is absent. */
    public record Defaults(Mode mode, Integer maxRepositories, Integer rolloutPercent) { }

    public record Worker(String workflow, boolean enabled, Mode maxMode) { }

    public record Campaign(
            boolean enabled,
            Mode mode,
            Integer maxRepositories,
            Integer rolloutPercent,
            Map<String, Mode> targets,
            Map<String, Worker> workers) { }
}
