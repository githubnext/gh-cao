package org.cao.backend.policy;

import java.util.List;
import java.util.Map;

/** Validated subset of {@code .github/workflows/cao.json} control-plane policy (version 1). */
public record ControlPolicy(
        String digest,
        Scope scope,
        Defaults defaults,
        Inventory inventory,
        Map<String, Campaign> campaigns) {

    /** Owner and repository allow-lists; empty means "not restricted by this list". */
    public record Scope(List<String> owners, List<String> repositories) { }

    /** Nullable members mean the declaration is absent. */
    public record Defaults(Mode mode, Integer maxRepositories, Integer rolloutPercent) { }

    /** Resolved scan, cell and batch ceilings (§5.3); schema defaults are already applied. */
    public record Inventory(int maxScanRepositories, int cellCount, int cellIndex, int batchSize, int batchIndex) {
        public static Inventory defaults() {
            return new Inventory(1000, 1, 0, 100_000, 0);
        }
    }

    public record Worker(String workflow, boolean enabled, Mode maxMode) { }

    public record Campaign(
            boolean enabled,
            Mode mode,
            Integer maxRepositories,
            Integer rolloutPercent,
            Map<String, Mode> targets,
            Map<String, Worker> workers) { }
}
