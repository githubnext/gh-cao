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
import java.time.Duration;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import org.cao.backend.ratelimit.RateLimitResult;
import org.cao.backend.ratelimit.RequestLimiter;
import org.cao.backend.ratelimit.TokenBucket;

/**
 * PostgreSQL adapter for the operational {@link AuditLog}. It owns only {@code cao_operational_*}
 * tables and opens pre-provisioned state: {@link #open} never runs DDL, and the schema owner
 * provisions storage through {@link OperationalBootstrap}. Appends serialize on the namespace row
 * and use database time, matching {@code server/internal/operational/postgres}.
 */
public final class PostgresOperationalStore implements AuditLog, RequestLimiter, AutoCloseable {
    /** Bounds audit persistence, mirroring operational.AuditWriteTimeout. */
    static final int WRITE_TIMEOUT_SECONDS = 5;
    private static final int MAX_CONNECTIONS = 4;
    /** Protected-state ceilings from {@link #DEFAULT_CONFIG}. */
    private static final long MAX_PROTECTED_ENTRIES = 200_000;
    private static final long MAX_PROTECTED_BYTES = 134_217_728;
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

    /**
     * One namespace-locked transaction under database time: reads the bucket, advances it, charges
     * protected capacity for a new or grown record and writes it back with an expiry of two refill
     * periods (specs/server-rate-limiting.md §3). A bucket that cannot be stored fails closed.
     */
    @Override
    public RateLimitResult take(String name, long capacity, long cost, Duration period,
        Duration deadline) {
        RequestLimiter.validate(name, capacity, cost, period, deadline);
        return transact(deadline, c -> {
            Instant now = databaseTime(c);
            byte[] stored = limiterBucket(c, name, now);
            TokenBucket bucket = stored == null ? new TokenBucket(capacity, now)
                : LimiterRecord.decode(stored);
            TokenBucket.Transition next = bucket.advance(now, capacity, cost, period);
            storeLimiterBucket(c, name, stored, LimiterRecord.encode(next.bucket()),
                next.bucket().at().plus(period.multipliedBy(2)));
            return next.result();
        });
    }

    private byte[] limiterBucket(Connection c, String name, Instant now) throws SQLException {
        try (PreparedStatement s = c.prepareStatement("SELECT value FROM cao_operational_records "
            + "WHERE namespace = ? AND kind = 'limiter' AND name = ? AND field = '' "
            + "AND (expires_at IS NULL OR expires_at > ?)")) {
            s.setString(1, namespace);
            s.setString(2, name);
            s.setObject(3, OffsetDateTime.ofInstant(now, java.time.ZoneOffset.UTC));
            try (ResultSet r = s.executeQuery()) {
                return r.next() ? r.getBytes(1) : null;
            }
        }
    }

    /** Charges the namespace's protected capacity by the size delta; exceeding it fails closed. */
    private void storeLimiterBucket(Connection c, String name, byte[] previous, byte[] value,
        Instant expires) throws SQLException {
        long overhead = 128 + "limiter".length() + name.getBytes(StandardCharsets.UTF_8).length;
        long existing = 0;
        long added = 1;
        try (PreparedStatement s = c.prepareStatement("SELECT octet_length(value) FROM "
            + "cao_operational_records WHERE namespace = ? AND kind = 'limiter' AND name = ? "
            + "AND field = ''")) {
            s.setString(1, namespace);
            s.setString(2, name);
            try (ResultSet r = s.executeQuery()) {
                if (r.next()) {
                    existing = r.getLong(1) + overhead;
                    added = 0;
                }
            }
        }
        long delta = overhead + value.length - existing;
        try (PreparedStatement s = c.prepareStatement("UPDATE cao_operational_namespaces SET "
            + "protected_bytes = protected_bytes + ?, protected_entries = protected_entries + ? "
            + "WHERE namespace = ? AND protected_bytes + ? <= ? AND protected_entries + ? <= ?")) {
            s.setLong(1, delta);
            s.setLong(2, added);
            s.setString(3, namespace);
            s.setLong(4, delta);
            s.setLong(5, MAX_PROTECTED_BYTES);
            s.setLong(6, added);
            s.setLong(7, MAX_PROTECTED_ENTRIES);
            if (s.executeUpdate() == 0) {
                throw new OperationalStoreException("operational protected-state capacity exhausted");
            }
        }
        try (PreparedStatement s = c.prepareStatement("INSERT INTO cao_operational_records"
            + "(namespace, kind, name, field, value, expires_at) VALUES (?, 'limiter', ?, '', ?, ?) "
            + "ON CONFLICT (namespace, kind, name, field) DO UPDATE SET value = EXCLUDED.value, "
            + "expires_at = EXCLUDED.expires_at")) {
            s.setString(1, namespace);
            s.setString(2, name);
            s.setBytes(3, value);
            s.setObject(4, OffsetDateTime.ofInstant(expires, java.time.ZoneOffset.UTC));
            s.executeUpdate();
        }
        if (previous == null && added == 1) {
            purgeExpiredLimiters(c);
        }
    }

    /**
     * Removes up to 512 idle buckets whose retention elapsed, releasing their protected capacity, so
     * unbounded distinct subjects cannot exhaust capacity forever. Active buckets are never evicted.
     */
    private void purgeExpiredLimiters(Connection c) throws SQLException {
        try (PreparedStatement s = c.prepareStatement("WITH gone AS (DELETE FROM "
            + "cao_operational_records WHERE ctid IN (SELECT ctid FROM cao_operational_records "
            + "WHERE namespace = ? AND kind = 'limiter' AND expires_at <= clock_timestamp() "
            + "ORDER BY expires_at LIMIT 512) RETURNING octet_length(value) + 128 + octet_length(kind) "
            + "+ octet_length(name) + octet_length(field) AS size) "
            + "UPDATE cao_operational_namespaces SET "
            + "protected_bytes = protected_bytes - coalesce((SELECT sum(size) FROM gone), 0), "
            + "protected_entries = protected_entries - (SELECT count(*) FROM gone) "
            + "WHERE namespace = ?")) {
            s.setString(1, namespace);
            s.setString(2, namespace);
            s.executeUpdate();
        }
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
        return transact(null, work);
    }

    /**
     * As {@link #transact(Work)} with an optional caller deadline: connection wait, lock wait and
     * statement time are all bounded by it, so a stalled database fails the call instead of delaying it.
     */
    private <T> T transact(Duration deadline, Work<T> work) {
        long millis = deadline == null ? WRITE_TIMEOUT_SECONDS * 1000L : Math.max(1, deadline.toMillis());
        int seconds = (int) Math.max(1, (millis + 999) / 1000);
        return withConnection(deadline, c -> {
            c.setAutoCommit(false);
            try (PreparedStatement bound = c.prepareStatement(
                "SELECT set_config('lock_timeout', ?, true), set_config('statement_timeout', ?, true)")) {
                bound.setString(1, Long.toString(millis));
                bound.setString(2, Long.toString(millis));
                bound.execute();
            }
            try (PreparedStatement lock = c.prepareStatement(
                "SELECT namespace FROM cao_operational_namespaces WHERE namespace = ? FOR UPDATE")) {
                lock.setString(1, namespace);
                lock.setQueryTimeout(seconds);
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

    private <T> T withConnection(Work<T> work) {
        return withConnection(null, work);
    }

    /**
     * At most four concurrent connections; permits return even when the driver fails. A non-null
     * {@code deadline} also bounds the permit wait and the driver's connect and socket timeouts.
     */
    private <T> T withConnection(Duration deadline, Work<T> work) {
        if (closed) {
            throw new OperationalStoreException("operational store is closed");
        }
        long millis = deadline == null ? WRITE_TIMEOUT_SECONDS * 1000L : Math.max(1, deadline.toMillis());
        try {
            if (!connections.tryAcquire(millis, TimeUnit.MILLISECONDS)) {
                throw new OperationalStoreException("operational store connection limit reached");
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new OperationalStoreException("interrupted while waiting for a connection", e);
        }
        Properties properties = endpoint.connectionProperties();
        if (deadline != null) {
            int seconds = (int) Math.max(1, (millis + 999) / 1000);
            properties.setProperty("connectTimeout", Integer.toString(Math.min(5, seconds)));
            properties.setProperty("socketTimeout", Integer.toString(Math.min(30, seconds + 1)));
        }
        try (Connection c = DriverManager.getConnection(endpoint.jdbcUrl(), properties)) {
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
