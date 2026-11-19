package org.cao.backend.canonical;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.HexFormat;
import java.util.Map;
import java.util.TreeMap;

/** Builds temporary synthetic snapshot directories with correct (or deliberately wrong) digests. */
final class SnapshotFixture {
    static final String RUNS = "gh-aw-logs-runs/synthetic.jsonl";
    static final String RECORDS = "gh-aw-logs-records/synthetic.jsonl";
    private static final ObjectMapper JSON = new ObjectMapper();

    private final Path directory;
    private final Map<String, String> hashes = new TreeMap<>();

    private SnapshotFixture(Path directory) {
        this.directory = directory;
    }

    static SnapshotFixture create(Path directory) throws IOException {
        return new SnapshotFixture(directory);
    }

    /** Copies the checked-in synthetic run shard. */
    SnapshotFixture withDefaultRuns() throws IOException {
        try (InputStream in = SnapshotFixture.class.getResourceAsStream(
            "snapshot/" + RUNS)) {
            return shard(RUNS, new String(in.readAllBytes(), StandardCharsets.UTF_8));
        }
    }

    SnapshotFixture shard(String name, String content) throws IOException {
        Path file = directory.resolve(name);
        Files.createDirectories(file.getParent());
        Files.writeString(file, content, StandardCharsets.UTF_8);
        hashes.put(name, sha256(content.getBytes(StandardCharsets.UTF_8)));
        return this;
    }

    SnapshotFixture claimedHash(String name, String hash) {
        hashes.put(name, hash);
        return this;
    }

    Path write() throws IOException {
        ObjectNode manifest = JSON.createObjectNode();
        hashes.forEach(manifest::put);
        Files.writeString(directory.resolve("payload-hashes.json"), JSON.writeValueAsString(manifest));
        return directory;
    }

    Path writeRawManifest(String json) throws IOException {
        Files.writeString(directory.resolve("payload-hashes.json"), json);
        return directory;
    }

    static String sha256(byte[] bytes) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
        } catch (java.security.NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
