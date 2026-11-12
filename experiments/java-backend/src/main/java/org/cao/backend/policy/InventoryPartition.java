package org.cao.backend.policy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.TreeMap;

/**
 * Deterministic repository inventory partitioning (specs/control-architecture.md §5.3: "Inventory
 * partitioning MUST be deterministic for the same inventory and effective values"). The inventory is
 * caller-supplied evidence; this class never discovers repositories.
 */
public final class InventoryPartition {
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private InventoryPartition() { }

    /** One enrolled repository with its immutable numeric identity. */
    public record Repository(long id, String fullName, boolean archived, boolean disabled) { }

    /**
     * Partition outcome.
     *
     * @param version {@code sha256:} digest of the canonical sorted inventory; not byte-compatible with the
     *     JavaScript digest, which hashes every GitHub API field it received
     */
    public record Result(String version, int inventoryCount, int cellRepositoryCount, int batchCount,
            String batchId, ControlPolicy.Inventory settings, List<Repository> candidates) { }

    public static Result partition(List<Repository> repositories, ControlPolicy.Inventory settings,
            boolean explicitTarget) {
        if (repositories == null || settings == null) {
            throw new PolicyException("inventory evidence is required");
        }
        if (repositories.size() > settings.maxScanRepositories()) {
            throw new PolicyException("inventory exceeds max-scan-repositories");
        }
        // Repositories are unique by numeric identity; later duplicates replace earlier ones.
        Map<Long, Repository> unique = new TreeMap<>();
        for (Repository repository : repositories) {
            if (repository == null || repository.id() < 1 || repository.fullName() == null
                    || repository.fullName().isBlank()) {
                throw new PolicyException("inventory repositories need a positive id and a full name");
            }
            unique.put(repository.id(), repository);
        }
        List<Repository> sorted = unique.values().stream()
                .sorted(Comparator.comparingLong(Repository::id).thenComparing(Repository::fullName)).toList();
        String version = digest(sorted);
        List<Repository> cell = explicitTarget ? sorted : sorted.stream()
                .filter(repository -> repository.id() % settings.cellCount() == settings.cellIndex()).toList();
        int batchCount = (cell.size() + settings.batchSize() - 1) / settings.batchSize();
        if (batchCount > 0 && settings.batchIndex() >= batchCount) {
            throw new PolicyException("batch-index must be smaller than the selected cell batch count");
        }
        int from = (int) Math.min((long) settings.batchIndex() * settings.batchSize(), cell.size());
        int to = (int) Math.min((long) from + settings.batchSize(), cell.size());
        String batchId = version + ":cell-" + settings.cellIndex() + "-of-" + settings.cellCount()
                + ":batch-" + settings.batchIndex() + "-of-" + batchCount;
        return new Result(version, sorted.size(), cell.size(), batchCount, batchId, settings,
                List.copyOf(cell.subList(from, to)));
    }

    private static String digest(List<Repository> sorted) {
        StringBuilder input = new StringBuilder();
        for (Repository repository : sorted) {
            ObjectNode node = MAPPER.createObjectNode();
            node.put("id", repository.id());
            node.put("full_name", repository.fullName().toLowerCase(Locale.ROOT));
            node.put("archived", repository.archived());
            node.put("disabled", repository.disabled());
            input.append(node).append('\n');
        }
        try {
            return "sha256:" + HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256")
                    .digest(input.toString().getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
