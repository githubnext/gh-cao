package org.cao.backend.inventory;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.Collection;
import java.util.Comparator;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * Deterministic cell and batch partitioning of a bounded inventory
 * (specs/control-architecture.md §5.3: "Inventory partitioning MUST be deterministic for the same
 * inventory and effective values"). The version digest covers every unique repository, sorted by id,
 * before cell selection.
 */
public final class InventoryPartitioner {
    private InventoryPartitioner() { }

    public record Limits(int cellCount, int cellIndex, int batchSize, int batchIndex) { }

    public record Partition(
            String version,
            int repositoryCount,
            int cellCount,
            int cellIndex,
            int cellRepositoryCount,
            int batchSize,
            int batchIndex,
            int batchCount,
            String batchId,
            List<InventoryRepository> candidates) { }

    public static Partition partition(Collection<InventoryRepository> repositories, Limits limits) {
        if (limits.cellCount() < 1 || limits.cellCount() > 1000
                || limits.cellIndex() < 0 || limits.cellIndex() >= limits.cellCount()
                || limits.batchSize() < 1 || limits.batchSize() > 100_000 || limits.batchIndex() < 0) {
            throw new InventoryException("cell and batch limits are out of range");
        }
        List<InventoryRepository> sorted = unique(repositories);
        String version = digest(sorted);
        List<InventoryRepository> cell = sorted.stream()
                .filter(repository -> repository.id() % limits.cellCount() == limits.cellIndex())
                .toList();
        int batchCount = (int) Math.ceil(cell.size() / (double) limits.batchSize());
        if (batchCount > 0 && limits.batchIndex() >= batchCount) {
            throw new InventoryException("batch_index must be smaller than the selected cell batch count ("
                    + batchCount + ")");
        }
        long start = (long) limits.batchIndex() * limits.batchSize();
        int from = (int) Math.min(start, cell.size());
        int to = (int) Math.min(start + limits.batchSize(), cell.size());
        return new Partition(version, sorted.size(), limits.cellCount(), limits.cellIndex(), cell.size(),
                limits.batchSize(), limits.batchIndex(), batchCount,
                version + ":cell-" + limits.cellIndex() + "-of-" + limits.cellCount()
                        + ":batch-" + limits.batchIndex() + "-of-" + batchCount,
                List.copyOf(cell.subList(from, to)));
    }

    /**
     * Identical duplicates collapse. Conflicting evidence for one id, or one name under two ids, is
     * ambiguous repository identity and fails closed rather than letting the last entry win.
     */
    static List<InventoryRepository> unique(Collection<InventoryRepository> repositories) {
        Map<Long, InventoryRepository> byId = new HashMap<>();
        Map<String, Long> byName = new HashMap<>();
        for (InventoryRepository repository : repositories) {
            InventoryRepository previous = byId.putIfAbsent(repository.id(), repository);
            if (previous != null && !previous.equals(repository)) {
                throw new InventoryException("conflicting inventory evidence for repository id " + repository.id());
            }
            Long owner = byName.putIfAbsent(repository.fullName().toLowerCase(Locale.ROOT), repository.id());
            if (owner != null && owner != repository.id()) {
                throw new InventoryException("one repository name appears under two ids");
            }
        }
        return byId.values().stream().sorted(Comparator.comparingLong(InventoryRepository::id)).toList();
    }

    static String digest(List<InventoryRepository> sorted) {
        StringBuilder input = new StringBuilder();
        for (InventoryRepository repository : sorted) {
            input.append(repository.canonicalJson()).append('\n');
        }
        try {
            byte[] hash = MessageDigest.getInstance("SHA-256")
                    .digest(input.toString().getBytes(StandardCharsets.UTF_8));
            return "sha256:" + HexFormat.of().formatHex(hash);
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
