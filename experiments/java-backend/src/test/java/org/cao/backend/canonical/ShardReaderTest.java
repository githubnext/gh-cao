package org.cao.backend.canonical;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ShardReaderTest {
    private static final String META = "{\"kind\":\"metadata\",\"phase\":\"runs\",\"records\":%d}\n";
    private static final String REPO =
        "{\"kind\":\"record\",\"collection\":\"repositories\",\"record\":{\"id\":\"repository:a/b\","
            + "\"observedAt\":\"2026-02-01T00:00:00Z\"}}\n";

    @TempDir
    Path dir;

    private List<CanonicalRecord> read(String content, boolean runPhase, int maxLine, java.util.function.BooleanSupplier cancel)
        throws Exception {
        Path file = dir.resolve("s.jsonl");
        Files.writeString(file, content);
        List<CanonicalRecord> out = new ArrayList<>();
        new ShardReader(maxLine, cancel).read(file, "s.jsonl", runPhase,
            SnapshotFixture.sha256(content.getBytes()), out::add);
        return out;
    }

    private String reject(String content, boolean runPhase) throws Exception {
        return assertThrows(SnapshotException.class,
            () -> read(content, runPhase, 1 << 20, () -> false)).getMessage();
    }

    @Test
    void readsRecordsWithRelationshipKeysAndNewestInstant() throws Exception {
        String run = "{\"kind\":\"record\",\"collection\":\"runs\",\"record\":{\"id\":\"run:1\","
            + "\"repositoryId\":\"repository:a/b\",\"workflowId\":\"workflow:w\","
            + "\"createdAt\":\"2026-02-02T01:00:00+01:00\",\"completedAt\":\"2026-02-02T00:30:00Z\"}}\n";
        List<CanonicalRecord> rows = read(String.format(META, 2) + REPO + run, true, 1 << 20, () -> false);
        assertEquals(2, rows.size());
        assertEquals("repositories", rows.get(0).collection());
        assertEquals(Instant.parse("2026-02-01T00:00:00Z"), rows.get(0).observed().orElseThrow());
        assertEquals("workflow:w", rows.get(1).workflowId());
        assertEquals(Instant.parse("2026-02-02T00:30:00Z"), rows.get(1).observed().orElseThrow());
    }

    @Test
    void metadataHeaderAndCountAreMandatory() throws Exception {
        assertTrue(reject(REPO, true).contains("precedes the metadata header"));
        assertTrue(reject(String.format(META, 2) + REPO, true).contains("does not match its metadata"));
        assertTrue(reject(String.format(META, 0) + REPO, true).contains("does not match its metadata"));
        assertTrue(reject(String.format(META, 1) + String.format(META, 1) + REPO, true)
            .contains("metadata must be the first"));
        assertTrue(reject("{\"kind\":\"metadata\",\"records\":-1}\n", true).contains("non-negative"));
        assertTrue(reject("{\"kind\":\"metadata\",\"records\":0,\"phase\":\"records\"}\n", true)
            .contains("phase"));
        assertTrue(reject("", true).contains("does not match its metadata"));
    }

    @Test
    void rejectsUnknownEnvelopesCollectionsAndPhases() throws Exception {
        assertTrue(reject(String.format(META, 1) + "{\"kind\":\"other\"}\n", true).contains("envelope"));
        assertTrue(reject(String.format(META, 1)
            + "{\"kind\":\"record\",\"collection\":\"nope\",\"record\":{\"id\":\"x\"}}\n", true)
            .contains("not a registered canonical collection"));
        assertTrue(reject("{\"kind\":\"metadata\",\"records\":1}\n" + REPO, false)
            .contains("wrong shard phase"));
        assertTrue(reject(String.format(META, 1) + REPO, false).contains("phase does not match"));
        String domain = "{\"kind\":\"record\",\"collection\":\"domains\",\"record\":{\"id\":\"d\"}}\n";
        assertTrue(reject("{\"kind\":\"metadata\",\"records\":1}\n" + domain, false)
            .contains("registered but not implemented"));
    }

    @Test
    void rejectsInvalidRecords() throws Exception {
        String head = String.format(META, 1);
        String repo = "{\"kind\":\"record\",\"collection\":\"repositories\",\"record\":%s}\n";
        assertTrue(reject(head + String.format(repo, "[]"), true).contains("must be an object"));
        assertTrue(reject(head + String.format(repo, "{}"), true).contains("id is required"));
        assertTrue(reject(head + String.format(repo, "{\"id\":\"\"}"), true).contains("non-empty"));
        assertTrue(reject(head + String.format(repo, "{\"id\":5}"), true).contains("non-empty"));
        assertTrue(reject(head + String.format(repo, "{\"id\":\"a\",\"observedAt\":\"yesterday\"}"), true)
            .contains("RFC 3339"));
        assertTrue(reject(head + String.format(repo, "{\"id\":\"a\",\"id\":\"b\"}"), true)
            .contains("valid canonical JSON"));
        assertTrue(reject(head + String.format(repo, "{\"id\":\"a\",\"note\":\"x\\u0000y\"}"), true)
            .contains("NUL"));
        assertTrue(reject(head + "not json\n", true).contains("s.jsonl:2"));
        String run = "{\"kind\":\"record\",\"collection\":\"runs\",\"record\":{\"id\":\"r\"}}\n";
        assertTrue(reject(head + run, true).contains("repositoryId is required"));
    }

    @Test
    void errorsDoNotEchoRecordContent() throws Exception {
        String message = reject(String.format(META, 1)
            + "{\"kind\":\"record\",\"collection\":\"repositories\",\"record\":{\"id\":\"a\","
            + "\"observedAt\":\"super-secret-token\"}}\n", true);
        assertTrue(!message.contains("super-secret-token"), message);
    }

    @Test
    void enforcesLineBoundAndCancellationAndDigest() throws Exception {
        String content = String.format(META, 1) + REPO;
        assertTrue(assertThrows(SnapshotException.class, () -> read(content, true, 40, () -> false))
            .getMessage().contains("byte limit"));
        assertThrows(IngestCancelledException.class, () -> read(content, true, 1 << 20, () -> true));
        Path file = dir.resolve("t.jsonl");
        Files.writeString(file, content);
        assertTrue(assertThrows(SnapshotException.class, () -> new ShardReader(1 << 20, () -> false)
            .read(file, "t.jsonl", true, "0".repeat(64), r -> { })).getMessage().contains("hash mismatch"));
    }
}
