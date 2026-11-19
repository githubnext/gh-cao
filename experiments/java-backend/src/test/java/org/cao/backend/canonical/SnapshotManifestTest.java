package org.cao.backend.canonical;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class SnapshotManifestTest {
    private static final String HASH = "a".repeat(64);

    @TempDir
    Path dir;

    private void rejected(String json, String fragment) throws Exception {
        SnapshotFixture.create(dir).writeRawManifest(json);
        SnapshotException e = assertThrows(SnapshotException.class, () -> SnapshotManifest.read(dir));
        assertTrue(e.getMessage().contains(fragment), e.getMessage());
    }

    @Test
    void acceptsRunAndRecordShardsAndComputesStableRevision() throws Exception {
        SnapshotFixture.create(dir).withDefaultRuns().shard(SnapshotFixture.RECORDS, "x").write();
        SnapshotManifest manifest = SnapshotManifest.read(dir);
        assertEquals(List.of(SnapshotFixture.RUNS), manifest.runShards());
        assertEquals(List.of(SnapshotFixture.RECORDS), manifest.recordShards());
        assertTrue(manifest.dataRevision().startsWith("sha256:"));
        assertEquals(manifest.dataRevision(), SnapshotManifest.read(dir).dataRevision());
    }

    @Test
    void revisionChangesWithAnyDigest() throws Exception {
        SnapshotFixture.create(dir).withDefaultRuns().write();
        String first = SnapshotManifest.read(dir).dataRevision();
        SnapshotFixture.create(dir).shard(SnapshotFixture.RUNS, "different").write();
        assertNotEquals(first, SnapshotManifest.read(dir).dataRevision());
    }

    @Test
    void rejectsUnsafeAndMalformedManifests() throws Exception {
        rejected("{}", "empty");
        rejected("[]", "object");
        rejected("not json", "valid JSON");
        rejected("{\"gh-aw-logs-runs/a.jsonl\":\"" + HASH + "\",\"gh-aw-logs-runs/a.jsonl\":\"" + HASH
            + "\"}", "valid JSON");
        rejected("{\"../x\":\"" + HASH + "\"}", "unsafe");
        rejected("{\"/abs\":\"" + HASH + "\"}", "unsafe");
        rejected("{\"gh-aw-logs-runs/../../x.jsonl\":\"" + HASH + "\"}", "unsafe");
        rejected("{\"gh-aw-logs-runs\\\\a.jsonl\":\"" + HASH + "\"}", "unsafe");
        rejected("{\"gh-aw-logs-runs/a.jsonl\":\"short\"}", "SHA-256");
        rejected("{\"gh-aw-logs-runs/a.jsonl\":7}", "SHA-256");
    }

    @Test
    void rejectsRawActivityShardsAndMissingRunShards() throws Exception {
        rejected("{\"gh-aw-logs-shards/a.jsonl\":\"" + HASH + "\"}", "raw activity JSONL");
        rejected("{\"gh-aw-logs-records/a.jsonl\":\"" + HASH + "\"}", "run-information");
    }

    @Test
    void verifyShardsDetectsTamperingAndMissingFiles() throws Exception {
        SnapshotFixture.create(dir).withDefaultRuns().write();
        SnapshotManifest manifest = SnapshotManifest.read(dir);
        manifest.verifyShards(dir);
        Files.writeString(dir.resolve(SnapshotFixture.RUNS), "tampered");
        assertThrows(SnapshotException.class, () -> manifest.verifyShards(dir));
        Files.delete(dir.resolve(SnapshotFixture.RUNS));
        assertThrows(SnapshotException.class, () -> manifest.verifyShards(dir));
    }

    @Test
    void shardSymlinksAreRefused() throws Exception {
        Path outside = Files.createTempFile("cao-outside", ".jsonl");
        try {
            Files.writeString(outside, "{}");
            SnapshotFixture.create(dir).claimedHash(SnapshotFixture.RUNS,
                SnapshotFixture.sha256("{}".getBytes())).write();
            Files.createDirectories(dir.resolve("gh-aw-logs-runs"));
            Files.createSymbolicLink(dir.resolve(SnapshotFixture.RUNS), outside);
            SnapshotManifest manifest = SnapshotManifest.read(dir);
            assertThrows(SnapshotException.class, () -> manifest.verifyShards(dir));
        } finally {
            Files.deleteIfExists(outside);
        }
    }
}
