package org.cao.backend.canonical;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.sql.Types;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.time.temporal.ChronoUnit;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicReference;
import org.cao.backend.operational.PostgresEndpoint;

/**
 * PostgreSQL adapter for {@link CanonicalStore}. Canonical data is kept apart from operational state:
 * only {@code cao_canonical_*} tables are touched. {@link #open} runs no DDL; the schema owner
 * provisions storage through {@link CanonicalBootstrap}.
 *
 * <p>Ingestion verifies every shard digest first, then replaces the dataset in one transaction that
 * holds the namespace state row ({@code FOR UPDATE}), so writers serialize, the revision
 * compare-and-swap is race-free, and any failure (invalid shard, duplicate identity, orphaned
 * relationship, cancellation) rolls back to the previously published dataset.
 */
public final class PostgresCanonicalStore implements CanonicalStore {
    private static final int BATCH = 1000;
    private static final int TIMEOUT_SECONDS = 60;
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final String[] COLLECTIONS = {"campaigns", "repositories", "workflows", "runs"};

    private final PostgresEndpoint endpoint;
    private final String namespace;

    private PostgresCanonicalStore(PostgresEndpoint endpoint, String namespace) {
        this.endpoint = endpoint;
        this.namespace = namespace;
    }

    static String requireNamespace(String namespace) {
        if (namespace == null || namespace.isBlank() || namespace.length() > 1024
            || namespace.indexOf('\0') >= 0) {
            throw new CanonicalStoreException("empty, oversized, or NUL-containing namespace");
        }
        return namespace;
    }

    /** Opens an existing, provisioned namespace without creating tables or state. */
    public static PostgresCanonicalStore open(PostgresEndpoint endpoint, String namespace) {
        PostgresCanonicalStore store = new PostgresCanonicalStore(endpoint, requireNamespace(namespace));
        store.state();
        return store;
    }

    public String namespace() {
        return namespace;
    }

    @FunctionalInterface
    private interface Work<T> {
        T run(Connection connection) throws SQLException;
    }

    private <T> T withConnection(boolean readOnly, Work<T> work) {
        try (Connection c = DriverManager.getConnection(endpoint.jdbcUrl(),
            endpoint.connectionProperties())) {
            c.setAutoCommit(false);
            if (readOnly) {
                c.setReadOnly(true);
                c.setTransactionIsolation(Connection.TRANSACTION_REPEATABLE_READ);
            }
            try (PreparedStatement bound = c.prepareStatement(
                "SELECT set_config('statement_timeout', ?, true), set_config('lock_timeout', ?, true)")) {
                String millis = Integer.toString(TIMEOUT_SECONDS * 1000);
                bound.setString(1, millis);
                bound.setString(2, millis);
                bound.execute();
            }
            try {
                T result = work.run(c);
                c.commit();
                return result;
            } catch (SQLException | RuntimeException e) {
                c.rollback();
                throw e;
            }
        } catch (SQLException e) {
            throw unavailable(e);
        }
    }

    private static CanonicalStoreException unavailable(SQLException e) {
        String state = e.getSQLState() == null ? "unknown" : e.getSQLState();
        return new CanonicalStoreException("canonical store unavailable (SQLSTATE " + state + ")");
    }

    @Override
    public CanonicalState state() {
        return withConnection(true, c -> readState(c, false));
    }

    private CanonicalState readState(Connection c, boolean lock) throws SQLException {
        try (PreparedStatement s = c.prepareStatement("SELECT revision, data_revision, evaluated_at, "
            + "schema_version FROM cao_canonical_state WHERE namespace = ?" + (lock ? " FOR UPDATE" : ""))) {
            s.setString(1, namespace);
            try (ResultSet r = s.executeQuery()) {
                if (!r.next()) {
                    throw new CanonicalStoreException("canonical namespace is not provisioned");
                }
                OffsetDateTime at = r.getObject(3, OffsetDateTime.class);
                return new CanonicalState(r.getLong(1), r.getString(2),
                    Optional.ofNullable(at).map(OffsetDateTime::toInstant));
            }
        }
    }

    @Override
    public Map<String, Long> counts() {
        return withConnection(true, this::counts);
    }

    @Override
    public IngestResult ingest(Path directory, IngestOptions options) {
        SnapshotManifest manifest = SnapshotManifest.read(directory);
        manifest.verifyShards(directory);
        String dataRevision = manifest.dataRevision();
        if (options.cancelled().getAsBoolean()) {
            throw new IngestCancelledException();
        }
        return withConnection(false, c -> publish(c, directory, manifest, dataRevision, options));
    }

    private IngestResult publish(Connection c, Path directory, SnapshotManifest manifest,
        String dataRevision, IngestOptions options) throws SQLException {
        CanonicalState current = readState(c, true);
        options.expectedRevision().ifPresent(expected -> {
            if (expected != current.revision()) {
                throw new StaleRevisionException(expected, current.revision());
            }
        });
        if (!options.force() && current.available() && current.dataRevision().equals(dataRevision)) {
            return new IngestResult(current, counts(c), false);
        }
        clear(c);
        AtomicReference<Instant> latest = new AtomicReference<>();
        Map<String, Long> ordinals = new LinkedHashMap<>();
        for (String collection : COLLECTIONS) {
            ordinals.put(collection, 0L);
        }
        Batcher batcher = new Batcher(c, ordinals);
        ShardReader reader = new ShardReader(options.maxLineBytes(), options.cancelled());
        try {
            for (boolean runPhase : new boolean[] {true, false}) {
                for (String name : runPhase ? manifest.runShards() : manifest.recordShards()) {
                    reader.read(manifest.resolve(directory, name), name, runPhase,
                        manifest.hashes().get(name), record -> {
                            record.observed().ifPresent(t -> latest.accumulateAndGet(t,
                                (a, b) -> a == null || b.isAfter(a) ? b : a));
                            batcher.add(record);
                        });
                    batcher.flush();
                }
            }
            try (Statement s = c.createStatement()) {
                s.execute("SET CONSTRAINTS ALL IMMEDIATE");
            }
            requireConsistentRepositories(c);
        } catch (SQLException e) {
            throw integrity(e);
        }
        if (options.cancelled().getAsBoolean()) {
            throw new IngestCancelledException();
        }
        Instant evaluated = latest.get() == null ? null : latest.get().truncatedTo(ChronoUnit.MICROS);
        long revision = current.revision() + 1;
        try (PreparedStatement s = c.prepareStatement("UPDATE cao_canonical_state SET revision = ?, "
            + "data_revision = ?, evaluated_at = ? WHERE namespace = ?")) {
            s.setLong(1, revision);
            s.setString(2, dataRevision);
            s.setObject(3, evaluated == null ? null : OffsetDateTime.ofInstant(evaluated, ZoneOffset.UTC),
                Types.TIMESTAMP_WITH_TIMEZONE);
            s.setString(4, namespace);
            s.executeUpdate();
        }
        return new IngestResult(new CanonicalState(revision, dataRevision, Optional.ofNullable(evaluated)),
            counts(c), true);
    }

    private void clear(Connection c) throws SQLException {
        for (int i = COLLECTIONS.length - 1; i >= 0; i--) {
            try (PreparedStatement s = c.prepareStatement(
                "DELETE FROM cao_canonical_" + COLLECTIONS[i] + " WHERE namespace = ?")) {
                s.setString(1, namespace);
                s.executeUpdate();
            }
        }
    }

    private Map<String, Long> counts(Connection c) throws SQLException {
        Map<String, Long> counts = new LinkedHashMap<>();
        for (String collection : COLLECTIONS) {
            try (PreparedStatement s = c.prepareStatement(
                "SELECT count(*) FROM cao_canonical_" + collection + " WHERE namespace = ?")) {
                s.setString(1, namespace);
                try (ResultSet r = s.executeQuery()) {
                    r.next();
                    counts.put(collection, r.getLong(1));
                }
            }
        }
        return counts;
    }

    /** A run must belong to the repository of its workflow (server/internal/postgresx publication check). */
    private void requireConsistentRepositories(Connection c) throws SQLException {
        try (PreparedStatement s = c.prepareStatement("SELECT 1 FROM cao_canonical_runs r JOIN "
            + "cao_canonical_workflows w ON w.namespace = r.namespace AND w.id = r.workflow_id "
            + "WHERE r.namespace = ? AND r.repository_id <> w.repository_id LIMIT 1")) {
            s.setString(1, namespace);
            try (ResultSet r = s.executeQuery()) {
                if (r.next()) {
                    throw new SnapshotException(
                        "a run references a repository different from its workflow's repository");
                }
            }
        }
    }

    private static RuntimeException integrity(SQLException e) {
        if ("23505".equals(e.getSQLState())) {
            return new SnapshotException("duplicate canonical identity or ordinal");
        }
        if ("23503".equals(e.getSQLState())) {
            return new SnapshotException(
                "a record references a missing parent (workflow, repository or campaign)");
        }
        if ("23514".equals(e.getSQLState()) || "22P05".equals(e.getSQLState())
            || "22021".equals(e.getSQLState())) {
            return new SnapshotException("a record violates a canonical storage constraint");
        }
        return unavailable(e);
    }

    /** Buffers rows per collection and writes them in bounded JDBC batches. */
    private final class Batcher {
        private final Connection c;
        private final Map<String, Long> ordinals;
        private final Map<String, PreparedStatement> statements = new LinkedHashMap<>();
        private final Map<String, Integer> pending = new LinkedHashMap<>();

        Batcher(Connection c, Map<String, Long> ordinals) {
            this.c = c;
            this.ordinals = ordinals;
        }

        void add(CanonicalRecord record) {
            try {
                PreparedStatement s = statements.get(record.collection());
                if (s == null) {
                    s = c.prepareStatement(insertSql(record.collection()));
                    statements.put(record.collection(), s);
                }
                long ordinal = ordinals.merge(record.collection(), 1L, Long::sum) - 1;
                int i = 1;
                s.setString(i++, namespace);
                s.setString(i++, record.id());
                s.setLong(i++, ordinal);
                if ("workflows".equals(record.collection())) {
                    s.setString(i++, record.repositoryId());
                    s.setString(i++, record.campaignId());
                } else if ("runs".equals(record.collection())) {
                    s.setString(i++, record.repositoryId());
                    s.setString(i++, record.workflowId());
                }
                s.setString(i, JSON.writeValueAsString(record.body()));
                s.addBatch();
                if (pending.merge(record.collection(), 1, Integer::sum) >= BATCH) {
                    flush(record.collection());
                }
            } catch (SQLException e) {
                throw integrity(e);
            } catch (JsonProcessingException e) {
                throw new SnapshotException("a record could not be serialized");
            }
        }

        void flush() throws SQLException {
            for (String collection : statements.keySet()) {
                flush(collection);
            }
        }

        private void flush(String collection) throws SQLException {
            if (pending.getOrDefault(collection, 0) > 0) {
                statements.get(collection).executeBatch();
                pending.put(collection, 0);
            }
        }
    }

    private static String insertSql(String collection) {
        return switch (collection) {
            case "campaigns", "repositories" -> "INSERT INTO cao_canonical_" + collection
                + "(namespace, id, ordinal, record) VALUES (?, ?, ?, ?::jsonb)";
            case "workflows" -> "INSERT INTO cao_canonical_workflows(namespace, id, ordinal, "
                + "repository_id, campaign_id, record) VALUES (?, ?, ?, ?, ?, ?::jsonb)";
            case "runs" -> "INSERT INTO cao_canonical_runs(namespace, id, ordinal, repository_id, "
                + "workflow_id, record) VALUES (?, ?, ?, ?, ?, ?::jsonb)";
            default -> throw new IllegalStateException("collection is not implemented: " + collection);
        };
    }
}
