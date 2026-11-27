package org.cao.backend.inventory;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import org.junit.jupiter.api.Test;

class InventoryPartitionerTest {
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private static InventoryRepository repo(long id, String name) {
        return new InventoryRepository(id, name, false, false, true, "2026-01-02T03:04:05Z", "main");
    }

    private static List<InventoryRepository> numbered(int count) {
        List<InventoryRepository> repositories = new ArrayList<>();
        for (int i = 1; i <= count; i++) {
            repositories.add(repo(i, "acme/r" + i));
        }
        return repositories;
    }

    private static List<String> names(InventoryPartitioner.Partition partition) {
        return partition.candidates().stream().map(InventoryRepository::fullName).toList();
    }

    @Test
    void digestAndCanonicalBytesMatchNodeGeneratedVectors() throws IOException {
        try (InputStream in = getClass().getResourceAsStream("js-inventory-digest-vectors.txt")) {
            String text = new String(in.readAllBytes(), StandardCharsets.UTF_8);
            int checked = 0;
            for (String line : text.split("\n", -1)) {
                if (line.isEmpty() || line.startsWith("#")) {
                    continue;
                }
                String[] parts = line.split("\t", -1);
                List<InventoryRepository> repositories = new ArrayList<>();
                List<String> expectedBytes = new ArrayList<>();
                for (String json : "-".equals(parts[1]) ? new String[0] : parts[1].split("\\|\\|\\|")) {
                    JsonNode node = MAPPER.readTree(json);
                    InventoryRepository repository = new InventoryRepository(node.get("id").asLong(),
                            node.get("full_name").asText(), node.get("archived").asBoolean(),
                            node.get("disabled").asBoolean(), node.get("private").asBoolean(),
                            node.get("pushed_at").isNull() ? null : node.get("pushed_at").asText(),
                            node.get("default_branch").isNull() ? null : node.get("default_branch").asText());
                    assertEquals(json, repository.canonicalJson());
                    repositories.add(repository);
                    expectedBytes.add(json);
                }
                Collections.reverse(repositories);
                assertEquals(parts[0], InventoryPartitioner.partition(repositories,
                        new InventoryPartitioner.Limits(1, 0, 100, 0)).version());
                checked++;
            }
            assertEquals(2, checked);
        }
    }

    @Test
    void cellsAreIdModuloAndBatchesAreContiguousSlices() {
        List<InventoryRepository> all = numbered(10);
        InventoryPartitioner.Partition cell = InventoryPartitioner.partition(all,
                new InventoryPartitioner.Limits(3, 1, 2, 0));
        assertEquals(List.of("acme/r1", "acme/r4"), names(cell));
        assertEquals(4, cell.cellRepositoryCount());
        assertEquals(2, cell.batchCount());
        assertEquals(10, cell.repositoryCount());
        assertTrue(cell.batchId().endsWith(":cell-1-of-3:batch-0-of-2"));
        assertEquals(List.of("acme/r7", "acme/r10"), names(InventoryPartitioner.partition(all,
                new InventoryPartitioner.Limits(3, 1, 2, 1))));
    }

    @Test
    void partitionIsIndependentOfInputOrderAndDuplicates() {
        List<InventoryRepository> all = numbered(7);
        List<InventoryRepository> shuffled = new ArrayList<>(all);
        Collections.reverse(shuffled);
        shuffled.add(all.get(2));
        InventoryPartitioner.Limits limits = new InventoryPartitioner.Limits(2, 0, 2, 1);
        assertEquals(InventoryPartitioner.partition(all, limits), InventoryPartitioner.partition(shuffled, limits));
    }

    @Test
    void cellsCoverEveryRepositoryExactlyOnce() {
        List<InventoryRepository> all = numbered(23);
        List<String> seen = new ArrayList<>();
        for (int cell = 0; cell < 4; cell++) {
            InventoryPartitioner.Partition first = InventoryPartitioner.partition(all,
                    new InventoryPartitioner.Limits(4, cell, 3, 0));
            for (int batch = 0; batch < first.batchCount(); batch++) {
                seen.addAll(names(InventoryPartitioner.partition(all,
                        new InventoryPartitioner.Limits(4, cell, 3, batch))));
            }
        }
        assertEquals(23, seen.size());
        assertEquals(23, seen.stream().distinct().count());
    }

    @Test
    void emptyCellHasZeroBatches() {
        InventoryPartitioner.Partition empty = InventoryPartitioner.partition(List.of(repo(2, "acme/a")),
                new InventoryPartitioner.Limits(2, 1, 5, 0));
        assertEquals(0, empty.batchCount());
        assertTrue(empty.candidates().isEmpty());
    }

    @Test
    void outOfRangeBatchAndInvalidLimitsFailClosed() {
        List<InventoryRepository> all = numbered(4);
        assertThrows(InventoryException.class, () -> InventoryPartitioner.partition(all,
                new InventoryPartitioner.Limits(1, 0, 2, 2)));
        assertThrows(InventoryException.class, () -> InventoryPartitioner.partition(all,
                new InventoryPartitioner.Limits(2, 2, 2, 0)));
        assertThrows(InventoryException.class, () -> InventoryPartitioner.partition(all,
                new InventoryPartitioner.Limits(0, 0, 2, 0)));
        assertThrows(InventoryException.class, () -> InventoryPartitioner.partition(all,
                new InventoryPartitioner.Limits(1, 0, 100_001, 0)));
    }

    @Test
    void conflictingIdentityEvidenceFailsClosed() {
        assertThrows(InventoryException.class, () -> InventoryPartitioner.partition(
                List.of(repo(1, "acme/a"), new InventoryRepository(1, "acme/a", true, false, true, null, null)),
                new InventoryPartitioner.Limits(1, 0, 10, 0)));
        assertThrows(InventoryException.class, () -> InventoryPartitioner.partition(
                List.of(repo(1, "acme/a"), repo(2, "ACME/a")), new InventoryPartitioner.Limits(1, 0, 10, 0)));
    }

    @Test
    void malformedRepositoryEvidenceIsRejected() {
        assertThrows(InventoryException.class, () -> repo(0, "acme/a"));
        assertThrows(InventoryException.class, () -> repo(1, "no-slash"));
        assertThrows(InventoryException.class, () -> repo(9_007_199_254_740_992L, "acme/a"));
    }
}
