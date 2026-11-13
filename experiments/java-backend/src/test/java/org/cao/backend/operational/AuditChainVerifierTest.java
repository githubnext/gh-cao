package org.cao.backend.operational;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class AuditChainVerifierTest {
    private static final Instant T = Instant.parse("2026-10-02T14:25:38.120Z");

    private static List<AuditChainVerifier.Row> chain(int count) {
        List<AuditChainVerifier.Row> rows = new ArrayList<>();
        byte[] prev = CanonicalAudit.genesis();
        for (int i = 1; i <= count; i++) {
            AuditEvent e = new AuditEvent(i, T.plusSeconds(i), "e" + i, "success", "actor",
                Map.of("n", Integer.toString(i)));
            byte[] record = CanonicalAudit.record(e);
            byte[] hash = CanonicalAudit.chainHash(prev, record);
            rows.add(new AuditChainVerifier.Row(i, e.occurredAt(), e.event(), e.outcome(), e.actor(),
                new String(record, StandardCharsets.UTF_8), prev, hash));
            prev = hash;
        }
        return rows;
    }

    private static AuditVerification verify(List<AuditChainVerifier.Row> rows, List<AuditAnchor> a) {
        AuditChainVerifier verifier = new AuditChainVerifier(a);
        rows.forEach(verifier::check);
        return verifier.finish();
    }

    @Test
    void verifiesIntactChain() {
        List<AuditChainVerifier.Row> rows = chain(3);
        AuditVerification result = verify(rows, List.of());
        assertEquals(3, result.events());
        assertEquals(1, result.firstSequence());
        assertEquals(3, result.lastSequence());
        assertEquals(CanonicalAudit.hex(rows.get(2).hash()), result.lastHash());
    }

    @Test
    void emptyChainVerifies() {
        assertEquals(0, verify(List.of(), List.of()).events());
    }

    @Test
    void detectsEditedRecord() {
        List<AuditChainVerifier.Row> rows = chain(3);
        AuditChainVerifier.Row r = rows.get(1);
        rows.set(1, new AuditChainVerifier.Row(r.sequence(), r.occurredAt(), r.event(), r.outcome(),
            r.actor(), r.record().replace("actor", "ac7or"), r.prevHash(), r.hash()));
        assertThrows(AuditTamperedException.class, () -> verify(rows, List.of()));
    }

    @Test
    void detectsIndexedColumnDivergingFromHashedRecord() {
        List<AuditChainVerifier.Row> rows = chain(2);
        AuditChainVerifier.Row r = rows.get(1);
        rows.set(1, new AuditChainVerifier.Row(r.sequence(), r.occurredAt(), "other", r.outcome(),
            r.actor(), r.record(), r.prevHash(), r.hash()));
        assertThrows(AuditTamperedException.class, () -> verify(rows, List.of()));
    }

    @Test
    void detectsGapAndBrokenChain() {
        List<AuditChainVerifier.Row> gap = chain(3);
        gap.remove(1);
        assertThrows(AuditTamperedException.class, () -> verify(gap, List.of()));

        // A row whose prev_hash and hash are self-consistent but do not extend its predecessor.
        List<AuditChainVerifier.Row> reordered = chain(3);
        AuditChainVerifier.Row second = reordered.get(1);
        byte[] foreign = CanonicalAudit.chainHash(new byte[32], new byte[] {1});
        reordered.set(1, new AuditChainVerifier.Row(second.sequence(), second.occurredAt(),
            second.event(), second.outcome(), second.actor(), second.record(), foreign,
            CanonicalAudit.chainHash(foreign, second.record().getBytes(StandardCharsets.UTF_8))));
        assertThrows(AuditTamperedException.class, () -> verify(reordered, List.of()));
    }

    @Test
    void retentionPrefixKeepsFirstRetainedPrevHashAsAnchor() {
        List<AuditChainVerifier.Row> rows = chain(4);
        List<AuditChainVerifier.Row> retained = rows.subList(2, 4);
        AuditVerification result = verify(retained, List.of(new AuditAnchor(1,
            CanonicalAudit.hex(rows.get(0).hash())), new AuditAnchor(3,
            CanonicalAudit.hex(rows.get(2).hash()))));
        assertEquals(2, result.events());
        assertEquals(3, result.firstSequence());
        assertEquals(1, result.anchorsChecked());
        assertEquals(1, result.anchorsExpired());
    }

    @Test
    void anchorMismatchAndTruncatedHeadAreTampering() {
        List<AuditChainVerifier.Row> rows = chain(3);
        assertThrows(AuditTamperedException.class,
            () -> verify(rows, List.of(new AuditAnchor(2, "0".repeat(64)))));
        List<AuditChainVerifier.Row> truncated = rows.subList(0, 2);
        assertThrows(AuditTamperedException.class, () -> verify(truncated,
            List.of(new AuditAnchor(3, CanonicalAudit.hex(rows.get(2).hash())))));
        assertThrows(AuditTamperedException.class, () -> verify(List.of(),
            List.of(new AuditAnchor(1, "0".repeat(64)))));
    }

    @Test
    void invalidOrConflictingAnchorsAreInputErrors() {
        assertThrows(AuditInputException.class,
            () -> new AuditChainVerifier(List.of(new AuditAnchor(0, "0".repeat(64)))));
        assertThrows(AuditInputException.class,
            () -> new AuditChainVerifier(List.of(new AuditAnchor(1, "zz"))));
        assertThrows(AuditInputException.class, () -> new AuditChainVerifier(
            List.of(new AuditAnchor(1, "0".repeat(64)), new AuditAnchor(1, "1".repeat(64)))));
    }
}
