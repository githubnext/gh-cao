package org.cao.backend.operational;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;

/**
 * PostgreSQL adapter for the operational {@link AuditLog}. It owns only {@code cao_operational_*}
 * tables and opens pre-provisioned state: {@link #open} never runs DDL, and the schema owner
 * provisions storage through {@link OperationalBootstrap}. Appends serialize on the namespace row
 * and use database time, matching {@code server/internal/operational/postgres}.
 */
public final class PostgresOperationalStore implements AuditLog, AutoCloseable {
    /** Bounds audit persistence, mirroring operational.AuditWriteTimeout. */
    static final int WRITE_TIMEOUT_SECONDS = 5;
    private static final int MAX_CONNECTIONS = 4;
    private static final ObjectMapper JSON = new ObjectMapper();

    /** Go's default capacity configuration, serialized exactly as it stores it. */
    static final byte[] DEFAULT_CONFIG = ("{\"MaxCacheEntries\":1024,\"MaxCacheBytes\":33554432,"
        + "\"MaxCacheValueBytes\":4194304,\"MaxProtectedEntries\":200000,"
        + "\"MaxProtectedBytes\":134217728,\"MaxRecordBytes\":1048576}")
        .getBytes(StandardCharsets.UTF_8);

    private final PostgresEndpoint endpoint;
    private final String namespace;
    private final Semaphore connections = new Semaphore(MAX_CONNECTIONS);
    private volatile boolean closed;

    private PostgresOperationalStore(PostgresEndpoint endpoint, String namespace) {
        this.endpoint = endpoint;
        this.namespace = namespace;
    }

    /** Validates {@code namespace}, shared with the Go adapter's name rules. */
    static String requireNamespace(String namespace) {
        if (namespace == null || namespace.isBlank() || namespace.length() > 1024
            || namespace.indexOf('\0') >= 0) {
            throw new OperationalStoreException("empty, oversized, or NUL-containing namespace");
        }
        return namespace;
    }

    /** Opens an existing, provisioned namespace without creating tables or state. */
    public static PostgresOperationalStore open(PostgresEndpoint endpoint, String namespace) {
        PostgresOperationalStore store = new PostgresOperationalStore(endpoint,
            requireNamespace(namespace));
        store.checkNamespace();
        return store;
    }

    public String namespace() {
        return namespace;
    }

    /** Read-only liveness plus provisioning check; unprovisioned or mismatched state fails. */
    public void ping() {
        checkNamespace();
    }

    private void checkNamespace() {
        withConnection(c -> {
            try (PreparedStatement s = c.prepareStatement(
                "SELECT version, config FROM cao_operational_namespaces WHERE namespace = ?")) {
                s.setString(1, namespace);
                s.setQueryTimeout(WRITE_TIMEOUT_SECONDS);
                try (ResultSet r = s.executeQuery()) {
                    if (!r.next()) {
                        throw new OperationalStoreException(
                            "operational namespace is not provisioned");
                    }
                    if (r.getInt(1) != 2) {
                        throw new OperationalStoreException(
                            "operational schema requires owner-run database bootstrap");
                    }
                    if (!Arrays.equals(r.getBytes(2), DEFAULT_CONFIG)) {
                        throw new OperationalStoreException(
                            "namespace capacity configuration differs from existing deployment");
                    }
                }
            }
            return null;
        });
    }

    @Override
    public AuditEvent append(AuditEvent event) {
        AuditRules.validate(event);
        return transact(c -> {
            byte[] prev = CanonicalAudit.genesis();
            long last = 0;
            try (PreparedStatement s = c.prepareStatement("SELECT sequence, hash FROM "
                + "cao_operational_audit_events WHERE namespace = ? ORDER BY sequence DESC LIMIT 1")) {
                s.setString(1, namespace);
                try (ResultSet r = s.executeQuery()) {
                    if (r.next()) {
                        last = r.getLong(1);
                        prev = r.getBytes(2);
                    }
                }
            }
            Instant now = databaseTime(c);
            AuditEvent stored = new AuditEvent(last + 1, now, event.event(), event.outcome(),
                event.actor(), event.attributes());
            byte[] record = CanonicalAudit.record(stored);
            try (PreparedStatement s = c.prepareStatement("INSERT INTO cao_operational_audit_events"
                + "(namespace, sequence, occurred_at, event, outcome, actor, record, prev_hash, hash) "
                + "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")) {
                s.setString(1, namespace);
                s.setLong(2, stored.sequence());
                s.setObject(3, OffsetDateTime.ofInstant(now, java.time.ZoneOffset.UTC));
                s.setString(4, stored.event());
                s.setString(5, stored.outcome());
                s.setString(6, stored.actor());
                s.setString(7, new String(record, StandardCharsets.UTF_8));
                s.setBytes(8, prev);
                s.setBytes(9, CanonicalAudit.chainHash(prev, record));
                s.executeUpdate();
            }
            return stored;
        });
    }

    @Override
    public List<AuditEvent> list(AuditFilter filter) {
        return withConnection(c -> {
            try (PreparedStatement s = c.prepareStatement("SELECT record FROM "
                + "cao_operational_audit_events WHERE namespace = ? AND sequence > ? "
                + "AND (? = '' OR event = ?) AND (? = '' OR actor = ?) ORDER BY sequence LIMIT ?")) {
                s.setString(1, namespace);
                s.setLong(2, filter.afterSequence());
                s.setString(3, filter.event());
                s.setString(4, filter.event());
                s.setString(5, filter.actor());
                s.setString(6, filter.actor());
                s.setInt(7, filter.effectiveLimit());
                s.setQueryTimeout(WRITE_TIMEOUT_SECONDS);
                List<AuditEvent> events = new ArrayList<>();
                try (ResultSet r = s.executeQuery()) {
                    while (r.next()) {
                        events.add(parse(r.getString(1)));
                    }
                }
                return events;
            }
        });
    }

    @Override
    public AuditVerification verify(List<AuditAnchor> anchors) {
        AuditChainVerifier verifier = new AuditChainVerifier(anchors);
        return withConnection(c -> {
            c.setAutoCommit(false);
            c.setReadOnly(true);
            c.setTransactionIsolation(Connection.TRANSACTION_REPEATABLE_READ);
            try (PreparedStatement s = c.prepareStatement("SELECT sequence, occurred_at, event, "
                + "outcome, actor, record, prev_hash, hash FROM cao_operational_audit_events "
                + "WHERE namespace = ? ORDER BY sequence")) {
                s.setString(1, namespace);
                s.setFetchSize(500);
                try (ResultSet r = s.executeQuery()) {
                    while (r.next()) {
                        verifier.check(new AuditChainVerifier.Row(r.getLong(1),
                            r.getObject(2, OffsetDateTime.class).toInstant(), r.getString(3),
                            r.getString(4), r.getString(5), r.getString(6), r.getBytes(7),
                            r.getBytes(8)));
                    }
                }
            }
            c.rollback();
            return verifier.finish();
        });
    }

    @Override
    public void close() {
        closed = true;
    }

    private static AuditEvent parse(String record) {
        try {
            JsonNode node = JSON.readTree(record);
            Map<String, String> attributes = new LinkedHashMap<>();
            node.path("attributes").fields()
                .forEachRemaining(e -> attributes.put(e.getKey(), e.getValue().asText()));
            return new AuditEvent(node.path("sequence").asLong(),
                Instant.parse(node.path("occurred_at").asText()), node.path("event").asText(),
                node.path("outcome").asText(), node.path("actor").asText(""), attributes);
        } catch (JsonProcessingException | java.time.format.DateTimeParseException e) {
            throw new OperationalStoreException("corrupt audit record", e);
        }
    }

    private static Instant databaseTime(Connection c) throws SQLException {
        try (Statement s = c.createStatement();
             ResultSet r = s.executeQuery("SELECT clock_timestamp()")) {
            r.next();
            return r.getObject(1, OffsetDateTime.class).toInstant().truncatedTo(ChronoUnit.MILLIS);
        }
    }

    @FunctionalInterface
    private interface Work<T> {
        T run(Connection connection) throws SQLException;
    }

    /** Locks the namespace row first, so every multi-record transition serializes. */
    private <T> T transact(Work<T> work) {
        return withConnection(c -> {
            c.setAutoCommit(false);
            try (PreparedStatement bound = c.prepareStatement(
                "SELECT set_config('lock_timeout', ?, true), set_config('statement_timeout', ?, true)")) {
                String millis = Integer.toString(WRITE_TIMEOUT_SECONDS * 1000);
                bound.setString(1, millis);
                bound.setString(2, millis);
                bound.execute();
            }
            try (PreparedStatement lock = c.prepareStatement(
                "SELECT namespace FROM cao_operational_namespaces WHERE namespace = ? FOR UPDATE")) {
                lock.setString(1, namespace);
                lock.setQueryTimeout(WRITE_TIMEOUT_SECONDS);
                try (ResultSet r = lock.executeQuery()) {
                    if (!r.next()) {
                        throw new OperationalStoreException(
                            "operational namespace is not provisioned");
                    }
                }
            }
            T result = work.run(c);
            c.commit();
            return result;
        });
    }

    /** At most four concurrent connections; permits return even when the driver fails. */
    private <T> T withConnection(Work<T> work) {
        if (closed) {
            throw new OperationalStoreException("operational store is closed");
        }
        try {
            if (!connections.tryAcquire(WRITE_TIMEOUT_SECONDS, TimeUnit.SECONDS)) {
                throw new OperationalStoreException("operational store connection limit reached");
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new OperationalStoreException("interrupted while waiting for a connection", e);
        }
        try (Connection c = DriverManager.getConnection(endpoint.jdbcUrl(),
            endpoint.connectionProperties())) {
            return work.run(c);
        } catch (SQLException e) {
            throw unavailable(e);
        } finally {
            connections.release();
        }
    }

    /** Never leaks driver messages, which can echo connection details, to callers. */
    private static OperationalStoreException unavailable(SQLException e) {
        String state = e.getSQLState() == null ? "unknown" : e.getSQLState();
        return new OperationalStoreException("operational store unavailable (SQLSTATE " + state + ")");
    }
}
