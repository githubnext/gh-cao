package org.cao.backend.operational;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * Verifies retained rows in ascending sequence order. Retention removes the oldest prefix, so the
 * first retained prev_hash is the anchor (comparison: AuditChainVerifier in audit.go).
 */
public final class AuditChainVerifier {
    private static final ObjectMapper JSON = new ObjectMapper();

    /** One stored row: separately indexed columns plus the exact hashed record. */
    public record Row(long sequence, Instant occurredAt, String event, String outcome, String actor,
                      String record, byte[] prevHash, byte[] hash) { }

    private final Map<Long, String> anchors = new HashMap<>();
    private long events;
    private long first;
    private long last;
    private byte[] lastHash;
    private int anchorsChecked;

    public AuditChainVerifier(List<AuditAnchor> anchorList) {
        for (AuditAnchor anchor : anchorList) {
            String hash = anchor.hash() == null ? "" : anchor.hash().strip().toLowerCase(Locale.ROOT);
            if (anchor.sequence() <= 0 || !hash.matches("[0-9a-f]{64}")) {
                throw new AuditInputException("audit anchor requires a positive sequence and a "
                    + "64-character hex SHA-256 hash");
            }
            String previous = anchors.put(anchor.sequence(), hash);
            if (previous != null && !previous.equals(hash)) {
                throw new AuditInputException(
                    "conflicting audit anchors for sequence " + anchor.sequence());
            }
        }
    }

    public void check(Row row) {
        long sequence = row.sequence();
        if (events > 0 && sequence != last + 1) {
            throw tampered(sequence, "sequence gap");
        }
        if (lastHash != null && !Arrays.equals(row.prevHash(), lastHash)) {
            throw tampered(sequence, "broken chain");
        }
        byte[] recordBytes = row.record().getBytes(StandardCharsets.UTF_8);
        if (!Arrays.equals(row.hash(), CanonicalAudit.chainHash(row.prevHash(), recordBytes))) {
            throw tampered(sequence, "hash mismatch");
        }
        JsonNode stored;
        try {
            stored = JSON.readTree(row.record());
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            throw tampered(sequence, "unreadable record");
        }
        if (stored == null || !stored.isObject() || !matches(stored, row)) {
            throw tampered(sequence, "indexed columns differ from the hashed record");
        }
        String anchor = anchors.get(sequence);
        if (anchor != null) {
            if (!anchor.equals(CanonicalAudit.hex(row.hash()))) {
                throw tampered(sequence, "hash differs from the external anchor");
            }
            anchorsChecked++;
        }
        if (events == 0) {
            first = sequence;
        }
        events++;
        last = sequence;
        lastHash = row.hash();
    }

    /** Reports anchors the retained chain cannot account for. */
    public AuditVerification finish() {
        int expired = 0;
        for (long sequence : anchors.keySet()) {
            if (events == 0 || sequence > last) {
                throw tampered(sequence, "anchored sequence is beyond the retained chain head");
            } else if (sequence < first) {
                expired++;
            }
        }
        return new AuditVerification(events, first, last,
            lastHash == null ? "" : CanonicalAudit.hex(lastHash), anchorsChecked, expired);
    }

    private static boolean matches(JsonNode stored, Row row) {
        try {
            return stored.path("sequence").asLong(-1) == row.sequence()
                && stored.path("occurred_at").isTextual()
                && Instant.parse(stored.get("occurred_at").asText()).equals(row.occurredAt())
                && stored.path("event").asText("\0").equals(row.event())
                && stored.path("outcome").asText("\0").equals(row.outcome())
                && stored.path("actor").asText("").equals(row.actor());
        } catch (java.time.format.DateTimeParseException e) {
            return false;
        }
    }

    private static AuditTamperedException tampered(long sequence, String reason) {
        return new AuditTamperedException("at sequence " + sequence + ": " + reason);
    }
}
