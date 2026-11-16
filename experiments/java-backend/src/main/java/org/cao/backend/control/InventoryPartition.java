package org.cao.backend.control;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.cao.backend.policy.ControlPolicy;
import org.cao.backend.policy.PolicyException;

/**
 * Deterministic inventory versioning, cell and batch selection (specs/control-architecture.md §5.3:
 * "Inventory partitioning MUST be deterministic for the same inventory and effective values").
 *
 * @param candidates the selected batch of the selected cell, ordered by repository id
 */
public record InventoryPartition(
        String version,
        int inventoryRepositoryCount,
        int cellCount,
        int cellIndex,
        int cellRepositoryCount,
        int batchSize,
        int batchIndex,
        int batchCount,
        String batchId,
        List<RepositoryRecord> candidates) {

    public static InventoryPartition of(List<RepositoryRecord> repositories, ControlPolicy.Inventory inventory) {
        Map<Long, RepositoryRecord> unique = new LinkedHashMap<>();
        for (RepositoryRecord repository : repositories) {
            unique.put(repository.id(), repository);
        }
        List<RepositoryRecord> sorted = new ArrayList<>(unique.values());
        sorted.sort(Comparator.comparingLong(RepositoryRecord::id));
        String version = digest(sorted);
        List<RepositoryRecord> cell = sorted.stream()
                .filter(repository -> repository.id() % inventory.cellCount() == inventory.cellIndex())
                .toList();
        int batchCount = (cell.size() + inventory.batchSize() - 1) / inventory.batchSize();
        if (batchCount > 0 && inventory.batchIndex() >= batchCount) {
            throw new PolicyException("batch_index must be smaller than the selected cell batch count ("
                    + batchCount + ")");
        }
        long from = (long) inventory.batchIndex() * inventory.batchSize();
        int start = (int) Math.min(from, cell.size());
        int end = (int) Math.min(from + inventory.batchSize(), cell.size());
        return new InventoryPartition(version, sorted.size(), inventory.cellCount(), inventory.cellIndex(),
                cell.size(), inventory.batchSize(), inventory.batchIndex(), batchCount,
                version + ":cell-" + inventory.cellIndex() + "-of-" + inventory.cellCount()
                        + ":batch-" + inventory.batchIndex() + "-of-" + batchCount,
                List.copyOf(cell.subList(start, end)));
    }

    private static String digest(List<RepositoryRecord> sorted) {
        StringBuilder input = new StringBuilder();
        for (RepositoryRecord repository : sorted) {
            input.append('{').append("\"id\":").append(repository.id())
                    .append(",\"full_name\":").append(JsonText.quote(repository.fullName()))
                    .append(",\"archived\":").append(repository.archived())
                    .append(",\"disabled\":").append(repository.disabled())
                    .append(",\"private\":").append(repository.isPrivate())
                    .append(",\"pushed_at\":").append(JsonText.quote(repository.pushedAt()))
                    .append(",\"default_branch\":").append(JsonText.quote(repository.defaultBranch()))
                    .append("}\n");
        }
        try {
            return "sha256:" + HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256")
                    .digest(input.toString().getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
