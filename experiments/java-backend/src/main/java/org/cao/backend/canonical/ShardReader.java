package org.cao.backend.canonical;

import com.fasterxml.jackson.core.StreamReadFeature;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.json.JsonMapper;
import com.fasterxml.jackson.databind.cfg.JsonNodeFeature;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.format.DateTimeParseException;
import java.util.HexFormat;
import java.util.Iterator;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;

/**
 * Streams one compacted canonical shard (server-ingestion.md profile, specs/dashboard-data.md
 * §32/§36): a mandatory {@code metadata} header, then {@code record} envelopes. Lines are bounded,
 * parsed one at a time and handed to the sink, so a shard is never materialized. The shard's content
 * digest is recomputed while reading and compared with the manifest at the end, so a file that changed
 * after verification cannot be ingested.
 */
final class ShardReader {
    private static final JsonMapper JSON = JsonMapper.builder()
        .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
        .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
        .enable(DeserializationFeature.USE_BIG_DECIMAL_FOR_FLOATS)
        .configure(JsonNodeFeature.STRIP_TRAILING_BIGDECIMAL_ZEROES, false).build();
    private static final List<String> TIMESTAMPS = List.of("observedAt", "timestamp", "createdAt",
        "startedAt", "completedAt", "updatedAt");

    private final int maxLineBytes;
    private final BooleanSupplier cancelled;

    ShardReader(int maxLineBytes, BooleanSupplier cancelled) {
        this.maxLineBytes = maxLineBytes;
        this.cancelled = cancelled;
    }

    /** Result of one fully read and verified shard. */
    record Summary(String name, String phase, long records, String digest) { }

    Summary read(Path file, String name, boolean runPhase, String expectedDigest,
        Consumer<CanonicalRecord> sink) {
        String phase = runPhase ? "runs" : "records";
        MessageDigest digest = sha256();
        long count = 0;
        long declared = -1;
        boolean header = false;
        long line = 0;
        try (InputStream in = new DigestInputStream(Files.newInputStream(file,
            LinkOption.NOFOLLOW_LINKS), digest)) {
            byte[] raw;
            while ((raw = nextLine(in, name)) != null) {
                line++;
                if (cancelled.getAsBoolean()) {
                    throw new IngestCancelledException();
                }
                if (blank(raw)) {
                    continue;
                }
                JsonNode envelope = parse(raw, name, line);
                String kind = text(envelope, "kind");
                if ("metadata".equals(kind)) {
                    if (header || count != 0) {
                        throw invalid(name, line, "metadata must be the first entry");
                    }
                    JsonNode records = envelope.get("records");
                    if (records == null || !records.isIntegralNumber() || !records.canConvertToLong()
                        || records.asLong() < 0) {
                        throw invalid(name, line, "metadata requires a non-negative record count");
                    }
                    JsonNode declaredPhase = envelope.get("phase");
                    if (declaredPhase != null && !(declaredPhase.isTextual()
                        && (declaredPhase.asText().isEmpty() || phase.equals(declaredPhase.asText())))) {
                        throw invalid(name, line, "metadata phase does not match its shard");
                    }
                    JsonNode version = envelope.get("schemaVersion");
                    if (version != null && !version.isIntegralNumber()) {
                        throw invalid(name, line, "metadata schemaVersion must be an integer");
                    }
                    declared = records.asLong();
                    header = true;
                } else if ("record".equals(kind)) {
                    if (!header) {
                        throw invalid(name, line, "record precedes the metadata header");
                    }
                    sink.accept(record(envelope, runPhase, name, line));
                    count++;
                } else {
                    throw invalid(name, line, "unsupported envelope kind");
                }
            }
        } catch (IOException e) {
            throw new SnapshotException("shard " + name + " is unreadable");
        }
        String actual = HexFormat.of().formatHex(digest.digest());
        if (!actual.equals(expectedDigest)) {
            throw new SnapshotException("payload hash mismatch while ingesting " + name);
        }
        if (!header || declared != count) {
            throw new SnapshotException("shard " + name + " record count does not match its metadata");
        }
        return new Summary(name, phase, count, actual);
    }

    private CanonicalRecord record(JsonNode envelope, boolean runPhase, String name, long line) {
        String collection = text(envelope, "collection");
        if (collection == null || !CanonicalCollections.registered(collection)) {
            throw invalid(name, line, "collection is not a registered canonical collection");
        }
        if (CanonicalCollections.runPhase(collection) != runPhase) {
            throw invalid(name, line, "collection is in the wrong shard phase");
        }
        if (!CanonicalCollections.IMPLEMENTED.contains(collection)) {
            throw invalid(name, line, "canonical collection '" + collection
                + "' is registered but not implemented by this backend");
        }
        JsonNode body = envelope.get("record");
        if (body == null || !body.isObject()) {
            throw invalid(name, line, "record must be an object");
        }
        if (containsNul(body, 0)) {
            throw invalid(name, line, "record contains a NUL character or is nested too deeply");
        }
        String id = required(body, "id", name, line);
        String repositoryId = null;
        String workflowId = null;
        String campaignId = null;
        switch (collection) {
            case "workflows" -> {
                repositoryId = required(body, "repositoryId", name, line);
                campaignId = optional(body, "campaignId", name, line);
            }
            case "runs" -> {
                repositoryId = required(body, "repositoryId", name, line);
                workflowId = required(body, "workflowId", name, line);
            }
            default -> { }
        }
        Instant observed = null;
        for (String field : TIMESTAMPS) {
            JsonNode value = body.get(field);
            if (value == null || value.isNull()) {
                continue;
            }
            Instant instant = instant(value, name, line, field);
            if (observed == null || instant.isAfter(observed)) {
                observed = instant;
            }
        }
        return new CanonicalRecord(collection, id, repositoryId, workflowId, campaignId, body,
            Optional.ofNullable(observed));
    }

    /** PostgreSQL JSONB cannot store U+0000; reject it explicitly instead of failing at insert. */
    private static boolean containsNul(JsonNode node, int depth) {
        if (depth > 64) {
            return true;
        }
        if (node.isTextual()) {
            return node.asText().indexOf('\0') >= 0;
        }
        for (Iterator<Map.Entry<String, JsonNode>> it = node.fields(); it.hasNext();) {
            Map.Entry<String, JsonNode> field = it.next();
            if (field.getKey().indexOf('\0') >= 0 || containsNul(field.getValue(), depth + 1)) {
                return true;
            }
        }
        if (node.isArray()) {
            for (JsonNode element : node) {
                if (containsNul(element, depth + 1)) {
                    return true;
                }
            }
        }
        return false;
    }

    private static Instant instant(JsonNode value, String name, long line, String field) {
        if (value.isTextual()) {
            try {
                return OffsetDateTime.parse(value.asText()).toInstant();
            } catch (DateTimeParseException e) {
                // falls through to the explicit rejection below
            }
        }
        throw invalid(name, line, field + " must be an RFC 3339 timestamp");
    }

    private static String required(JsonNode body, String field, String name, long line) {
        String value = optional(body, field, name, line);
        if (value == null) {
            throw invalid(name, line, field + " is required");
        }
        return value;
    }

    private static String optional(JsonNode body, String field, String name, long line) {
        JsonNode value = body.get(field);
        if (value == null || value.isNull()) {
            return null;
        }
        if (!value.isTextual() || value.asText().isBlank() || value.asText().length() > 2048
            || value.asText().indexOf('\0') >= 0) {
            throw invalid(name, line, field + " must be a non-empty identifier");
        }
        return value.asText();
    }

    private static String text(JsonNode node, String field) {
        JsonNode value = node.get(field);
        return value != null && value.isTextual() ? value.asText() : null;
    }

    private static JsonNode parse(byte[] raw, String name, long line) {
        try {
            JsonNode node = JSON.readTree(raw);
            if (node == null || !node.isObject()) {
                throw invalid(name, line, "entry must be a JSON object");
            }
            return node;
        } catch (IOException e) {
            throw invalid(name, line, "entry must contain valid canonical JSON");
        }
    }

    private static SnapshotException invalid(String name, long line, String reason) {
        return new SnapshotException(name + ":" + line + " " + reason);
    }

    private static boolean blank(byte[] raw) {
        for (byte b : raw) {
            if (b != ' ' && b != '\t' && b != '\r') {
                return false;
            }
        }
        return true;
    }

    /** Reads one newline-terminated line, failing before the buffer exceeds {@code maxLineBytes}. */
    private byte[] nextLine(InputStream in, String name) throws IOException {
        ByteArrayOutputStream buffer = new ByteArrayOutputStream();
        int next;
        boolean any = false;
        while ((next = in.read()) >= 0) {
            any = true;
            if (next == '\n') {
                return buffer.toByteArray();
            }
            if (buffer.size() >= maxLineBytes) {
                throw new SnapshotException("shard " + name + " has a line over the "
                    + maxLineBytes + " byte limit");
            }
            buffer.write(next);
        }
        return any ? buffer.toByteArray() : null;
    }

    private static MessageDigest sha256() {
        try {
            return MessageDigest.getInstance("SHA-256");
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
