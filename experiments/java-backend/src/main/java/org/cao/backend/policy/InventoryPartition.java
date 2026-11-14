package org.cao.backend.policy;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;

/**
 * Deterministic cell and batch partitioning of a repository inventory
 * (specs/control-architecture.md §5.3, §4.5): the same inventory and effective values always select the
 * same candidates, independent of discovery order.
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
        List<RepositoryMetadata> candidates) {

    /**
     * Repositories are de-duplicated by id (last occurrence wins), ordered by id, assigned to the cell where
     * {@code id mod cellCount == cellIndex}, then sliced into batches. A batch index at or beyond a non-empty
     * cell's batch count is fatal; an empty cell yields an empty batch.
     */
    public static InventoryPartition of(List<RepositoryMetadata> repositories, ControlPolicy.Inventory limits) {
        Map<Long, RepositoryMetadata> unique = new TreeMap<>();
        for (RepositoryMetadata repository : repositories) {
            unique.put(repository.id(), repository);
        }
        if (unique.size() > limits.maxScanRepositories()) {
            throw new PolicyException("inventory exceeds control-plane.inventory.max-scan-repositories");
        }
        List<RepositoryMetadata> sorted = List.copyOf(unique.values());
        String version = digest(sorted);
        List<RepositoryMetadata> cell = sorted.stream()
                .filter(repository -> repository.id() % limits.cellCount() == limits.cellIndex())
                .sorted(Comparator.comparingLong(RepositoryMetadata::id))
                .toList();
        int batchCount = (cell.size() + limits.batchSize() - 1) / limits.batchSize();
        if (batchCount > 0 && limits.batchIndex() >= batchCount) {
            throw new PolicyException("batch-index must be smaller than the selected cell batch count (" + batchCount + ")");
        }
        long from = Math.min((long) limits.batchIndex() * limits.batchSize(), cell.size());
        long to = Math.min(from + limits.batchSize(), cell.size());
        return new InventoryPartition(version, sorted.size(), limits.cellCount(), limits.cellIndex(), cell.size(),
                limits.batchSize(), limits.batchIndex(), batchCount,
                version + ":cell-" + limits.cellIndex() + "-of-" + limits.cellCount()
                        + ":batch-" + limits.batchIndex() + "-of-" + batchCount,
                List.copyOf(cell.subList((int) from, (int) to)));
    }

    static String digest(List<RepositoryMetadata> sorted) {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            for (RepositoryMetadata repository : sorted) {
                digest.update(repository.canonicalBytes());
                digest.update("\n".getBytes(StandardCharsets.UTF_8));
            }
            return "sha256:" + HexFormat.of().formatHex(digest.digest());
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
