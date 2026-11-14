package org.cao.backend.operational;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

/**
 * Exercises the real PostgreSQL adapter against a disposable loopback instance: owner bootstrap
 * versus runtime privileges, atomic append, hash chain, tamper detection and retention.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class PostgresOperationalStoreTest {
    private TestPostgres pg;
    private PostgresEndpoint owner;
    private PostgresEndpoint runtime;

    @BeforeAll
    void startDatabase() throws Exception {
        pg = TestPostgres.start();
        owner = PostgresEndpoint.parse(pg.url(TestPostgres.OWNER, "postgres"));
        try (Connection c = DriverManager.getConnection(owner.jdbcUrl(), owner.connectionProperties());
             Statement s = c.createStatement()) {
            s.execute("CREATE ROLE cao_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE");
            s.execute("REVOKE CREATE ON SCHEMA public FROM PUBLIC, cao_runtime");
        }
        runtime = PostgresEndpoint.parse(pg.url(TestPostgres.RUNTIME, "postgres"));
        OperationalBootstrap.provision(owner, "ns-main");
        // Mirrors the audit portion of server/postgres/operational-grants.sql.
        try (Connection c = DriverManager.getConnection(owner.jdbcUrl(), owner.connectionProperties());
             Statement s = c.createStatement()) {
            s.execute("GRANT SELECT ON cao_operational_namespaces TO cao_runtime");
            s.execute("GRANT UPDATE (protected_bytes, protected_entries) "
                + "ON cao_operational_namespaces TO cao_runtime");
            s.execute("GRANT SELECT, INSERT ON cao_operational_audit_events TO cao_runtime");
        }
    }

    @AfterAll
    void stopDatabase() throws Exception {
        pg.close();
    }

    private PostgresOperationalStore store(String namespace) {
        return PostgresOperationalStore.open(runtime, namespace);
    }

    private static Connection owner(PostgresEndpoint endpoint) throws SQLException {
        return DriverManager.getConnection(endpoint.jdbcUrl(), endpoint.connectionProperties());
    }

    private void provision(String namespace) throws SQLException {
        OperationalBootstrap.provision(owner, namespace);
    }

    @Test
    void runtimeOpenNeverCreatesStateAndFailsClosedWhenUnprovisioned() throws Exception {
        OperationalStoreException e = assertThrows(OperationalStoreException.class,
            () -> store("never-provisioned"));
        assertTrue(e.getMessage().contains("not provisioned"), e.getMessage());
        try (Connection c = owner(owner); Statement s = c.createStatement();
             ResultSet r = s.executeQuery("SELECT count(*) FROM cao_operational_namespaces "
                 + "WHERE namespace = 'never-provisioned'")) {
            r.next();
            assertEquals(0, r.getInt(1));
        }
    }

    @Test
    void runtimeLoginCannotRunDdlOrRewriteAuditRows() throws Exception {
        store("ns-main").append(AuditEvent.of("priv.check", "success", "u", Map.of()));
        try (Connection c = owner(runtime); Statement s = c.createStatement()) {
            assertThrows(SQLException.class, () -> s.execute("CREATE TABLE rogue(x int)"));
            assertThrows(SQLException.class,
                () -> s.execute("UPDATE cao_operational_audit_events SET actor = 'x'"));
            assertThrows(SQLException.class, () -> s.execute("DELETE FROM cao_operational_audit_events"));
            assertThrows(SQLException.class,
                () -> s.execute("DELETE FROM cao_operational_namespaces"));
            assertThrows(SQLException.class,
                () -> s.execute("SELECT cao_operational_retain_audit('ns-main')"));
        }
    }

    @Test
    void appendAssignsGapFreeSequenceDatabaseTimeAndChain() throws Exception {
        provision("ns-chain");
        PostgresOperationalStore store = store("ns-chain");
        Instant before = Instant.now().minusSeconds(5);
        AuditEvent first = store.append(AuditEvent.of("admin.rebuild.admitted", "success", "octocat",
            Map.of("rebuild_id", "r-1")));
        AuditEvent second = store.append(AuditEvent.of("admin.denied", "denied", "", Map.of()));
        assertEquals(1, first.sequence());
        assertEquals(2, second.sequence());
        assertTrue(first.occurredAt().isAfter(before));
        assertFalse(second.occurredAt().isBefore(first.occurredAt()));

        AuditVerification verification = store.verify(List.of());
        assertEquals(2, verification.events());
        assertEquals(1, verification.firstSequence());
        assertEquals(2, verification.lastSequence());
        assertEquals(64, verification.lastHash().length());

        List<AuditEvent> all = store.list(AuditFilter.all());
        assertEquals(List.of("admin.rebuild.admitted", "admin.denied"),
            all.stream().map(AuditEvent::event).toList());
        assertEquals(Map.of("rebuild_id", "r-1"), all.get(0).attributes());
        assertEquals(1, store.list(new AuditFilter(0, "admin.denied", "", 10)).size());
        assertEquals(1, store.list(new AuditFilter(0, "", "octocat", 10)).size());
        assertEquals(List.of(2L), store.list(new AuditFilter(1, "", "", 10)).stream()
            .map(AuditEvent::sequence).toList());
        assertEquals(1, store.list(new AuditFilter(0, "", "", 1)).size());

        // An external anchor of the head still verifies after later appends.
        AuditAnchor anchor = new AuditAnchor(2, verification.lastHash());
        store.append(AuditEvent.of("later", "failure", "", Map.of()));
        AuditVerification anchored = store.verify(List.of(anchor));
        assertEquals(3, anchored.events());
        assertEquals(1, anchored.anchorsChecked());
        assertNotEquals(verification.lastHash(), anchored.lastHash());
    }

    @Test
    void invalidEventsAreRejectedBeforeStorage() throws Exception {
        provision("ns-invalid");
        PostgresOperationalStore store = store("ns-invalid");
        assertThrows(AuditInputException.class,
            () -> store.append(AuditEvent.of("e", "unknown", "", Map.of())));
        assertThrows(AuditInputException.class,
            () -> store.append(AuditEvent.of("", "success", "", Map.of())));
        assertTrue(store.list(AuditFilter.all()).isEmpty());
        assertEquals(0, store.verify(List.of()).events());
    }

    @Test
    void concurrentAppendsSerializeOnTheNamespaceRow() throws Exception {
        provision("ns-concurrent");
        PostgresOperationalStore store = store("ns-concurrent");
        int count = 24;
        ExecutorService pool = Executors.newFixedThreadPool(8);
        try {
            List<Callable<AuditEvent>> work = new ArrayList<>();
            for (int i = 0; i < count; i++) {
                int n = i;
                work.add(() -> store.append(AuditEvent.of("concurrent", "success", "t" + n, Map.of())));
            }
            Set<Long> sequences = new HashSet<>();
            for (Future<AuditEvent> f : pool.invokeAll(work)) {
                sequences.add(f.get().sequence());
            }
            assertEquals(count, sequences.size());
            assertEquals(1L, sequences.stream().min(Long::compare).orElseThrow());
            assertEquals((long) count, sequences.stream().max(Long::compare).orElseThrow());
        } finally {
            pool.shutdownNow();
        }
        assertEquals(count, store.verify(List.of()).events());
    }

    @Test
    void namespacesAreIsolated() throws Exception {
        provision("ns-a");
        provision("ns-b");
        store("ns-a").append(AuditEvent.of("only.a", "success", "", Map.of()));
        PostgresOperationalStore b = store("ns-b");
        assertTrue(b.list(AuditFilter.all()).isEmpty());
        assertEquals(1, b.append(AuditEvent.of("only.b", "success", "", Map.of())).sequence());
        assertEquals(1, store("ns-a").list(AuditFilter.all()).size());
    }

    @Test
    void tamperingByTheOwnerIsDetectedByVerification() throws Exception {
        provision("ns-tamper");
        PostgresOperationalStore store = store("ns-tamper");
        for (int i = 0; i < 3; i++) {
            store.append(AuditEvent.of("e" + i, "success", "actor", Map.of()));
        }
        assertEquals(3, store.verify(List.of()).events());
        try (Connection c = owner(owner); Statement s = c.createStatement()) {
            // The append-only trigger rejects ordinary rewrites even for the owner.
            SQLException blocked = assertThrows(SQLException.class, () -> s.execute(
                "UPDATE cao_operational_audit_events SET actor = 'mallory' WHERE namespace = 'ns-tamper'"));
            assertTrue(blocked.getMessage().contains("append-only"), blocked.getMessage());
            // A privileged actor who disables the trigger is caught by the hash chain.
            s.execute("ALTER TABLE cao_operational_audit_events DISABLE TRIGGER cao_operational_audit_append_only");
            try {
                s.execute("UPDATE cao_operational_audit_events SET record = replace(record, 'actor', 'mallory')"
                    + " WHERE namespace = 'ns-tamper' AND sequence = 2");
            } finally {
                s.execute("ALTER TABLE cao_operational_audit_events ENABLE TRIGGER cao_operational_audit_append_only");
            }
        }
        assertThrows(AuditTamperedException.class, () -> store.verify(List.of()));
    }

    @Test
    void javaVerifiesRowsWrittenWithGoCanonicalBytes() throws Exception {
        provision("ns-go");
        String[] lines = new String(getClass().getResourceAsStream("go-audit-vectors.tsv").readAllBytes(),
            StandardCharsets.UTF_8).stripTrailing().split("\n");
        Instant[] times = {Instant.parse("2026-10-02T14:25:38.120Z"),
            Instant.parse("2026-10-02T14:25:39Z"), Instant.parse("2026-10-02T14:25:39.120Z"),
            Instant.parse("2026-10-02T14:25:40.120Z")};
        String[] names = {"admin.rebuild.admitted", "token.revoked", "x<y>&z", "e"};
        String[] outcomes = {"success", "denied", "failure", "success"};
        String[] actors = {"octocat", "", "ünï\u2028\u2029", ""};
        byte[] prev = CanonicalAudit.genesis();
        try (Connection c = owner(owner)) {
            for (int i = 0; i < lines.length; i++) {
                String[] columns = lines[i].split("\t");
                byte[] hash = CanonicalAudit.chainHash(prev, columns[1].getBytes(StandardCharsets.UTF_8));
                assertEquals(columns[2], CanonicalAudit.hex(hash));
                try (PreparedStatement s = c.prepareStatement("INSERT INTO cao_operational_audit_events"
                    + "(namespace, sequence, occurred_at, event, outcome, actor, record, prev_hash, hash) "
                    + "VALUES ('ns-go', ?, ?, ?, ?, ?, ?, ?, ?)")) {
                    s.setLong(1, i + 1);
                    s.setObject(2, OffsetDateTime.ofInstant(times[i], ZoneOffset.UTC));
                    s.setString(3, names[i]);
                    s.setString(4, outcomes[i]);
                    s.setString(5, actors[i]);
                    s.setString(6, columns[1]);
                    s.setBytes(7, prev);
                    s.setBytes(8, hash);
                    s.executeUpdate();
                }
                prev = hash;
            }
        }
        PostgresOperationalStore store = store("ns-go");
        AuditVerification verification = store.verify(List.of());
        assertEquals(4, verification.events());
        assertEquals("7e909e299b2fd9f92e77e8b94ba54530293ab74fb3b76fd7fceed01f1f481267",
            verification.lastHash());
        AuditEvent next = store.append(AuditEvent.of("java.appended", "success", "", Map.of()));
        assertEquals(5, next.sequence());
        assertEquals(5, store.verify(List.of()).events());
    }

    @Test
    void retentionKeepsNewestEventAndRemainsOwnerOnly() throws Exception {
        provision("ns-retain");
        Instant old = Instant.now().minusSeconds(500L * 24 * 3600).truncatedTo(java.time.temporal.ChronoUnit.MILLIS);
        Instant recent = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.MILLIS);
        byte[] prev = CanonicalAudit.genesis();
        try (Connection c = owner(owner)) {
            for (int i = 1; i <= 4; i++) {
                AuditEvent e = new AuditEvent(i, i <= 3 ? old.plusSeconds(i) : recent, "e" + i,
                    "success", "", Map.of());
                byte[] record = CanonicalAudit.record(e);
                byte[] hash = CanonicalAudit.chainHash(prev, record);
                try (PreparedStatement s = c.prepareStatement("INSERT INTO cao_operational_audit_events"
                    + "(namespace, sequence, occurred_at, event, outcome, actor, record, prev_hash, hash) "
                    + "VALUES ('ns-retain', ?, ?, ?, 'success', '', ?, ?, ?)")) {
                    s.setLong(1, i);
                    s.setObject(2, OffsetDateTime.ofInstant(e.occurredAt(), ZoneOffset.UTC));
                    s.setString(3, e.event());
                    s.setString(4, new String(record, StandardCharsets.UTF_8));
                    s.setBytes(5, prev);
                    s.setBytes(6, hash);
                    s.executeUpdate();
                }
                prev = hash;
            }
            try (Statement s = c.createStatement(); ResultSet r = s.executeQuery(
                "SELECT cao_operational_retain_audit('ns-retain')")) {
                r.next();
                assertEquals(3, r.getLong(1));
            }
            try (Statement s = c.createStatement(); ResultSet r = s.executeQuery(
                "SELECT cao_operational_retain_audit('ns-retain')")) {
                r.next();
                assertEquals(0, r.getLong(1));
            }
            try (Statement s = c.createStatement()) {
                assertThrows(SQLException.class,
                    () -> s.execute("SELECT cao_operational_retain_audit('missing-namespace')"));
                assertThrows(SQLException.class,
                    () -> s.execute("DELETE FROM cao_operational_audit_events WHERE namespace='ns-retain'"));
            }
        }
        AuditVerification verification = store("ns-retain").verify(List.of());
        assertEquals(1, verification.events());
        assertEquals(4, verification.firstSequence());
    }

    @Test
    void closedStoreFailsExplicitly() throws Exception {
        provision("ns-closed");
        PostgresOperationalStore store = store("ns-closed");
        store.close();
        assertThrows(OperationalStoreException.class,
            () -> store.append(AuditEvent.of("e", "success", "", Map.of())));
        assertThrows(OperationalStoreException.class, store::ping);
    }

    @Test
    void lostDatabaseReportsUnavailableNotSuccess() throws Exception {
        TestPostgres own = TestPostgres.start();
        try {
            PostgresEndpoint admin = PostgresEndpoint.parse(own.url(TestPostgres.OWNER, "postgres"));
            OperationalBootstrap.provision(admin, "ns-lost");
            PostgresOperationalStore store = PostgresOperationalStore.open(admin, "ns-lost");
            store.ping();
            own.stopServer();
            OperationalStoreException e = assertThrows(OperationalStoreException.class, store::ping);
            assertFalse(e.getMessage().contains("127.0.0.1"));
            assertThrows(OperationalStoreException.class,
                () -> store.append(AuditEvent.of("e", "success", "", Map.of())));
        } finally {
            own.close();
        }
    }
}
