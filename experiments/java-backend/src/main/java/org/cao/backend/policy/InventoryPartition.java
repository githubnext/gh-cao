package org.cao.backend.policy;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Deterministic cell/batch partition of a bounded inventory (specs/control-architecture.md §5.3:
 * "Inventory partitioning MUST be deterministic for the same inventory and effective values").
 */
public record InventoryPartition(
        List<RepositoryRecord> candidates,
        String version,
        int repositoryCount,
        int cellCount,
        int cellIndex,
        int cellRepositoryCount,
        int batchSize,
        int batchIndex,
        int batchCount) {

    public static InventoryPartition create(ControlPolicy.Inventory inventory, List<RepositoryRecord> repositories,
            boolean targetShortcut) {
        Map<Long, RepositoryRecord> unique = new LinkedHashMap<>();
        for (RepositoryRecord repository : repositories) {
            unique.put(repository.id(), repository);
        }
        List<RepositoryRecord> sorted = new ArrayList<>(unique.values());
        sorted.sort(Comparator.comparingLong(RepositoryRecord::id).thenComparing(RepositoryRecord::fullName));
        List<RepositoryRecord> cell = targetShortcut ? sorted : sorted.stream()
                .filter(repository -> repository.id() % inventory.cellCount() == inventory.cellIndex()).toList();
        int batchCount = (int) Math.ceil((double) cell.size() / inventory.batchSize());
        if (batchCount > 0 && inventory.batchIndex() >= batchCount) {
            throw new PolicyException(
                    "batch_index must be smaller than the selected cell batch count (" + batchCount + ")");
        }
        long from = (long) inventory.batchIndex() * inventory.batchSize();
        int start = (int) Math.min(from, cell.size());
        int end = (int) Math.min(from + inventory.batchSize(), cell.size());
        return new InventoryPartition(List.copyOf(cell.subList(start, end)), digest(sorted), sorted.size(),
                inventory.cellCount(), inventory.cellIndex(), cell.size(), inventory.batchSize(),
                inventory.batchIndex(), batchCount);
    }

    public String batchId() {
        return version + ":cell-" + cellIndex + "-of-" + cellCount + ":batch-" + batchIndex + "-of-" + batchCount;
    }

    /** SHA-256 over newline-terminated compact JSON rows in (id, full_name) order, as the Actions resolver does. */
    static String digest(List<RepositoryRecord> sorted) {
        StringBuilder input = new StringBuilder();
        for (RepositoryRecord repository : sorted) {
            input.append(RepositoryJson.compact(repository)).append('\n');
        }
        try {
            byte[] hash = MessageDigest.getInstance("SHA-256").digest(input.toString().getBytes(StandardCharsets.UTF_8));
            return "sha256:" + HexFormat.of().formatHex(hash);
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
