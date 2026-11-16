package org.cao.backend.control;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.util.List;
import org.cao.backend.policy.ControlPolicy;
import org.cao.backend.policy.PolicyException;
import org.junit.jupiter.api.Test;

class InventoryPartitionTest {
    private static RepositoryRecord repo(long id, String name) {
        return new RepositoryRecord(id, name, false, false, true, "2026-01-01T00:00:00Z", "main");
    }

    @Test
    void sameInventoryAndValuesGiveTheSamePartitionRegardlessOfInputOrder() {
        ControlPolicy.Inventory limits = new ControlPolicy.Inventory(100, 2, 1, 2, 0);
        InventoryPartition a = InventoryPartition.of(List.of(repo(3, "a/c"), repo(1, "a/a"), repo(5, "a/e"),
                repo(7, "a/g")), limits);
        InventoryPartition b = InventoryPartition.of(List.of(repo(7, "a/g"), repo(5, "a/e"), repo(1, "a/a"),
                repo(3, "a/c")), limits);
        assertEquals(a, b);
        assertEquals(List.of(1L, 3L), a.candidates().stream().map(RepositoryRecord::id).toList());
        assertEquals(2, a.batchCount());
    }

    @Test
    void duplicateIdsCollapseAndEveryFieldChangesTheVersion() {
        ControlPolicy.Inventory limits = ControlPolicy.Inventory.defaults();
        InventoryPartition one = InventoryPartition.of(List.of(repo(1, "a/a"), repo(1, "a/a")), limits);
        assertEquals(1, one.inventoryRepositoryCount());
        InventoryPartition archived = InventoryPartition.of(
                List.of(new RepositoryRecord(1, "a/a", true, false, true, "2026-01-01T00:00:00Z", "main")), limits);
        assertEquals(false, one.version().equals(archived.version()));
    }

    @Test
    void emptyCellHasNoBatchesAndOutOfRangeBatchIsRejected() {
        InventoryPartition empty = InventoryPartition.of(List.of(repo(1, "a/a")),
                new ControlPolicy.Inventory(100, 2, 0, 10, 0));
        assertEquals(0, empty.batchCount());
        assertEquals(List.of(), empty.candidates());
        assertThrows(PolicyException.class, () -> InventoryPartition.of(List.of(repo(1, "a/a")),
                new ControlPolicy.Inventory(100, 1, 0, 10, 1)));
    }

    @Test
    void invalidRepositoryMetadataIsRejected() {
        for (long id : new long[] {0, -4, Long.MAX_VALUE}) {
            assertThrows(InventoryUnavailableException.class, () -> repo(id, "a/a"));
        }
        assertThrows(InventoryUnavailableException.class, () -> repo(1, "not a repo"));
        assertThrows(InventoryUnavailableException.class, () -> repo(1, null));
    }

    @Test
    void jsonQuotingMatchesEcmaScriptStringify() {
        assertEquals("\"a\\\"b\\\\c\\n\\u0007\u2028<&>\"", JsonText.quote("a\"b\\c\n\u0007\u2028<&>"));
        assertEquals("\"\\ud800\"", JsonText.quote("\ud800"));
        assertEquals("\"\ud83d\ude00\"", JsonText.quote("\ud83d\ude00"));
        assertEquals("null", JsonText.quote(null));
    }
}
