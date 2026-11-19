package org.cao.backend.canonical;

import com.fasterxml.jackson.core.StreamReadFeature;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.json.JsonMapper;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.TreeMap;
import java.util.regex.Pattern;

/**
 * A verified-input manifest ({@code payload-hashes.json}, specs/dashboard-data.md §36.1-36.2,
 * server/internal/ingest). Every entry is a safe relative path mapped to a SHA-256 digest. Compacted
 * run shards live under {@code gh-aw-logs-runs/} and record shards under {@code gh-aw-logs-records/};
 * raw activity JSONL ({@code gh-aw-logs-shards/}) is rejected, not converted.
 */
public record SnapshotManifest(Map<String, String> hashes, List<String> runShards,
    List<String> recordShards) {
    static final String MANIFEST = "payload-hashes.json";
    static final String RUNS_PREFIX = "gh-aw-logs-runs/";
    static final String RECORDS_PREFIX = "gh-aw-logs-records/";
    private static final String RAW_PREFIX = "gh-aw-logs-shards/";
    private static final long MAX_MANIFEST_BYTES = 4L << 20;
    private static final Pattern SHA256 = Pattern.compile("[0-9a-fA-F]{64}");
    private static final JsonMapper JSON = JsonMapper.builder()
        .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
        .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS).build();

    /** Reads and validates the manifest; it does not yet read any shard. */
    public static SnapshotManifest read(Path directory) {
        Path file = directory.resolve(MANIFEST);
        byte[] content;
        try (InputStream in = Files.newInputStream(file, LinkOption.NOFOLLOW_LINKS)) {
            content = in.readNBytes((int) MAX_MANIFEST_BYTES + 1);
        } catch (IOException e) {
            throw new SnapshotException("payload manifest is unreadable");
        }
        if (content.length > MAX_MANIFEST_BYTES) {
            throw new SnapshotException("payload manifest exceeds the size limit");
        }
        JsonNode root;
        try {
            root = JSON.readTree(content);
        } catch (IOException e) {
            throw new SnapshotException("payload manifest is not valid JSON");
        }
        if (root == null || !root.isObject()) {
            throw new SnapshotException("payload manifest must be a JSON object");
        }
        if (root.isEmpty()) {
            throw new SnapshotException("payload manifest is empty");
        }
        Map<String, String> hashes = new TreeMap<>();
        List<String> runs = new ArrayList<>();
        List<String> records = new ArrayList<>();
        for (Iterator<Map.Entry<String, JsonNode>> it = root.fields(); it.hasNext();) {
            Map.Entry<String, JsonNode> entry = it.next();
            String name = entry.getKey();
            requireSafePath(name);
            if (!entry.getValue().isTextual() || !SHA256.matcher(entry.getValue().asText()).matches()) {
                throw new SnapshotException("manifest hash for an entry is not SHA-256");
            }
            hashes.put(name, entry.getValue().asText().toLowerCase(Locale.ROOT));
            if (name.startsWith(RAW_PREFIX)) {
                throw new SnapshotException(
                    "raw activity JSONL is not supported; compacted run/record shards are required");
            } else if (name.startsWith(RUNS_PREFIX) && name.endsWith(".jsonl")) {
                runs.add(name);
            } else if (name.startsWith(RECORDS_PREFIX) && name.endsWith(".jsonl")) {
                records.add(name);
            }
        }
        if (runs.isEmpty()) {
            throw new SnapshotException(
                "activity shard manifest is missing compacted run-information shards");
        }
        return new SnapshotManifest(Map.copyOf(hashes), List.copyOf(runs.stream().sorted().toList()),
            List.copyOf(records.stream().sorted().toList()));
    }

    private static void requireSafePath(String name) {
        boolean unsafe = name.isEmpty() || name.startsWith("/") || name.contains("\\")
            || name.indexOf('\0') >= 0 || name.endsWith("/");
        if (!unsafe) {
            for (String segment : name.split("/", -1)) {
                if (segment.isEmpty() || ".".equals(segment) || "..".equals(segment)) {
                    unsafe = true;
                    break;
                }
            }
        }
        if (unsafe) {
            throw new SnapshotException("manifest path is unsafe");
        }
    }

    /**
     * A digest of the claimed content identity: the sorted manifest entries. Deterministic and
     * independent of the snapshot's location. It is not the Go server's revision: that also covers
     * {@code inventory-sources.json} and repository memory, which this implementation does not read yet.
     */
    public String dataRevision() {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            digest.update("cao-java-canonical-snapshot:1\0".getBytes(StandardCharsets.UTF_8));
            for (Map.Entry<String, String> e : new TreeMap<>(hashes).entrySet()) {
                digest.update((e.getKey() + "\0" + e.getValue() + "\0").getBytes(StandardCharsets.UTF_8));
            }
            return "sha256:" + HexFormat.of().formatHex(digest.digest());
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    /** Resolves a shard inside {@code directory}, refusing symlinks and non-regular files. */
    Path resolve(Path directory, String name) {
        Path root;
        try {
            root = directory.toRealPath();
        } catch (IOException e) {
            throw new SnapshotException("snapshot directory is unreadable");
        }
        Path file = root.resolve(name).normalize();
        if (!file.startsWith(root) || !Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS)) {
            throw new SnapshotException("manifested payload is missing or not a regular file");
        }
        return file;
    }

    /** Hashes every shard before any is parsed, so an unverified snapshot reaches no database work. */
    public void verifyShards(Path directory) {
        for (List<String> phase : List.of(runShards, recordShards)) {
            for (String name : phase) {
                if (!hashes.get(name).equals(sha256(resolve(directory, name)))) {
                    throw new SnapshotException("payload hash mismatch for a manifested shard");
                }
            }
        }
    }

    private static String sha256(Path file) {
        try (InputStream in = new DigestInputStream(Files.newInputStream(file,
            LinkOption.NOFOLLOW_LINKS), MessageDigest.getInstance("SHA-256"))) {
            in.transferTo(OutputStream.nullOutputStream());
            return HexFormat.of().formatHex(((DigestInputStream) in).getMessageDigest().digest());
        } catch (IOException | NoSuchAlgorithmException e) {
            throw new SnapshotException("manifested payload is unreadable");
        }
    }
}
