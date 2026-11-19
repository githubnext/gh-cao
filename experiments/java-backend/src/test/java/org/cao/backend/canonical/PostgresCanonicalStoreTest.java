package org.cao.backend.canonical;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Callable;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.atomic.AtomicInteger;
import org.cao.backend.operational.PostgresEndpoint;
import org.cao.backend.operational.TestPostgres;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;
import org.junit.jupiter.api.io.TempDir;

/** Runs against a disposable loopback PostgreSQL started by the test; never skipped. */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class PostgresCanonicalStoreTest {
    private static final String REPO_API = "{\"kind\":\"record\",\"collection\":\"repositories\","
        + "\"record\":{\"id\":\"repository:example/api\"}}\n";

    private TestPostgres pg;
    private PostgresEndpoint owner;
    private PostgresEndpoint runtime;
    private int namespaces;

    @TempDir
    Path dir;

    @BeforeAll
    void startDatabase() throws Exception {
        pg = TestPostgres.start();
        owner = pg.endpoint();
        try (Connection c = DriverManager.getConnection(owner.jdbcUrl(), owner.connectionProperties());
             Statement s = c.createStatement()) {
            s.execute("CREATE ROLE cao_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE");
            s.execute("REVOKE CREATE ON SCHEMA public FROM PUBLIC, cao_runtime");
        }
        runtime = PostgresEndpoint.parse(pg.url(TestPostgres.RUNTIME, "postgres"));
        CanonicalBootstrap.provision(owner, "bootstrap");
        try (Connection c = DriverManager.getConnection(owner.jdbcUrl(), owner.connectionProperties());
             Statement s = c.createStatement()) {
            s.execute("GRANT SELECT, UPDATE ON cao_canonical_state TO cao_runtime");
            for (String table : List.of("campaigns", "repositories", "workflows", "runs")) {
                s.execute("GRANT SELECT, INSERT, DELETE ON cao_canonical_" + table + " TO cao_runtime");
            }
        }
    }

    @AfterAll
    void stopDatabase() throws Exception {
        pg.close();
    }

    private String freshNamespace() {
        String namespace = "ns-" + namespaces++;
        CanonicalBootstrap.provision(owner, namespace);
        return namespace;
    }

    private PostgresCanonicalStore store(String namespace) {
        return PostgresCanonicalStore.open(runtime, namespace);
    }

    private Path snapshot(String name) throws Exception {
        Path target = Files.createDirectories(dir.resolve(name));
        return SnapshotFixture.create(target).withDefaultRuns().write();
    }

    private long scalar(String sql) throws SQLException {
        try (Connection c = DriverManager.getConnection(owner.jdbcUrl(), owner.connectionProperties());
             Statement s = c.createStatement(); ResultSet r = s.executeQuery(sql)) {
            r.next();
            return r.getLong(1);
        }
    }

    @Test
    void ingestPublishesVerifiedSnapshotAtomically() throws Exception {
        PostgresCanonicalStore store = store(freshNamespace());
        assertFalse(store.state().available());
        IngestResult result = store.ingest(snapshot("happy"), IngestOptions.defaults());
        assertTrue(result.replaced());
        assertEquals(1, result.state().revision());
        assertEquals(Map.of("campaigns", 1L, "repositories", 2L, "workflows", 1L, "runs", 2L),
            result.counts());
        assertEquals(result.counts(), store.counts());
        // observedAt/createdAt/... maximum across rows: run:1001 observedAt 2026-01-02T09:05:30Z
        // beats run:1002 createdAt 2026-01-03T09:00:00+01:00 (= 08:00Z on the 3rd), which is newer.
        assertEquals(Instant.parse("2026-01-03T08:00:00Z"), store.state().evaluatedAt().orElseThrow());
        assertTrue(store.state().dataRevision().startsWith("sha256:"));
    }

    @Test
    void identicalContentIsIdempotentUnlessForced() throws Exception {
        PostgresCanonicalStore store = store(freshNamespace());
        Path snapshot = snapshot("idempotent");
        IngestResult first = store.ingest(snapshot, IngestOptions.defaults());
        IngestResult again = store.ingest(snapshot, IngestOptions.defaults());
        assertFalse(again.replaced());
        assertEquals(first.state(), again.state());
        assertEquals(first.counts(), again.counts());
        IngestResult forced = store.ingest(snapshot, IngestOptions.defaults().withForce());
        assertTrue(forced.replaced());
        assertEquals(2, forced.state().revision());
        assertEquals(first.counts(), forced.counts());
    }

    @Test
    void compareAndSwapRejectsStaleExpectedRevision() throws Exception {
        PostgresCanonicalStore store = store(freshNamespace());
        Path snapshot = snapshot("cas");
        assertThrows(StaleRevisionException.class,
            () -> store.ingest(snapshot, IngestOptions.defaults().withExpectedRevision(1)));
        assertFalse(store.state().available());
        store.ingest(snapshot, IngestOptions.defaults().withExpectedRevision(0));
        assertThrows(StaleRevisionException.class,
            () -> store.ingest(snapshot, IngestOptions.defaults().withExpectedRevision(0).withForce()));
        assertEquals(1, store.state().revision());
    }

    @Test
    void concurrentWritersWithTheSameExpectedRevisionHaveExactlyOneWinner() throws Exception {
        PostgresCanonicalStore store = store(freshNamespace());
        Path snapshot = snapshot("race");
        ExecutorService pool = Executors.newFixedThreadPool(4);
        try {
            CountDownLatch start = new CountDownLatch(1);
            AtomicInteger wins = new AtomicInteger();
            AtomicInteger stale = new AtomicInteger();
            List<Future<?>> futures = new java.util.ArrayList<>();
            for (int i = 0; i < 4; i++) {
                Callable<Void> task = () -> {
                    start.await();
                    try {
                        store.ingest(snapshot, IngestOptions.defaults().withExpectedRevision(0).withForce());
                        wins.incrementAndGet();
                    } catch (StaleRevisionException e) {
                        stale.incrementAndGet();
                    }
                    return null;
                };
                futures.add(pool.submit(task));
            }
            start.countDown();
            for (Future<?> f : futures) {
                f.get();
            }
            assertEquals(1, wins.get());
            assertEquals(3, stale.get());
            assertEquals(1, store.state().revision());
            assertEquals(2L, store.counts().get("runs"));
        } finally {
            pool.shutdownNow();
        }
    }

    private void assertPreviousDatasetServes(PostgresCanonicalStore store, IngestResult published,
        Path bad, Class<? extends RuntimeException> expected) {
        assertThrows(expected, () -> store.ingest(bad, IngestOptions.defaults()));
        assertEquals(published.state(), store.state());
        assertEquals(published.counts(), store.counts());
    }

    @Test
    void failedIngestionsLeaveThePreviousDatasetServing() throws Exception {
        PostgresCanonicalStore store = store(freshNamespace());
        IngestResult published = store.ingest(snapshot("base"), IngestOptions.defaults());

        // Orphan: a workflow referencing a repository that is not in the snapshot.
        Path orphan = SnapshotFixture.create(Files.createDirectories(dir.resolve("orphan")))
            .shard(SnapshotFixture.RUNS, "{\"kind\":\"metadata\",\"records\":1}\n"
                + "{\"kind\":\"record\",\"collection\":\"workflows\",\"record\":{\"id\":\"workflow:x\","
                + "\"repositoryId\":\"repository:missing\"}}\n").write();
        assertPreviousDatasetServes(store, published, orphan, SnapshotException.class);

        // Duplicate identity.
        Path duplicate = SnapshotFixture.create(Files.createDirectories(dir.resolve("dup")))
            .shard(SnapshotFixture.RUNS, "{\"kind\":\"metadata\",\"records\":2}\n" + REPO_API + REPO_API)
            .write();
        assertPreviousDatasetServes(store, published, duplicate, SnapshotException.class);

        // Run whose repository differs from its workflow's repository.
        Path mismatch = SnapshotFixture.create(Files.createDirectories(dir.resolve("mismatch")))
            .shard(SnapshotFixture.RUNS, "{\"kind\":\"metadata\",\"records\":4}\n" + REPO_API
                + "{\"kind\":\"record\",\"collection\":\"repositories\",\"record\":{\"id\":\"repository:example/web\"}}\n"
                + "{\"kind\":\"record\",\"collection\":\"workflows\",\"record\":{\"id\":\"workflow:w\","
                + "\"repositoryId\":\"repository:example/api\"}}\n"
                + "{\"kind\":\"record\",\"collection\":\"runs\",\"record\":{\"id\":\"run:z\","
                + "\"repositoryId\":\"repository:example/web\",\"workflowId\":\"workflow:w\"}}\n").write();
        assertPreviousDatasetServes(store, published, mismatch, SnapshotException.class);

        // Unverifiable digest and unsupported (registered but unimplemented) collection.
        Path tampered = SnapshotFixture.create(Files.createDirectories(dir.resolve("tampered")))
            .shard(SnapshotFixture.RUNS, "{\"kind\":\"metadata\",\"records\":0}\n")
            .claimedHash(SnapshotFixture.RUNS, "0".repeat(64)).write();
        assertPreviousDatasetServes(store, published, tampered, SnapshotException.class);
        Path unsupported = SnapshotFixture.create(Files.createDirectories(dir.resolve("unsupported")))
            .withDefaultRuns().shard(SnapshotFixture.RECORDS, "{\"kind\":\"metadata\",\"records\":1}\n"
                + "{\"kind\":\"record\",\"collection\":\"audits\",\"record\":{\"id\":\"a\"}}\n").write();
        assertPreviousDatasetServes(store, published, unsupported, SnapshotException.class);
    }

    @Test
    void cancellationBeforePublicationRollsBack() throws Exception {
        PostgresCanonicalStore store = store(freshNamespace());
        Path snapshot = snapshot("cancel");
        AtomicInteger polls = new AtomicInteger();
        assertThrows(IngestCancelledException.class, () -> store.ingest(snapshot,
            IngestOptions.defaults().withCancellation(() -> polls.incrementAndGet() > 4)));
        assertFalse(store.state().available());
        assertEquals(0L, store.counts().get("runs"));
    }

    @Test
    void namespacesAreIsolated() throws Exception {
        PostgresCanonicalStore one = store(freshNamespace());
        PostgresCanonicalStore two = store(freshNamespace());
        one.ingest(snapshot("iso"), IngestOptions.defaults());
        assertFalse(two.state().available());
        assertEquals(0L, two.counts().get("runs"));
        Path small = SnapshotFixture.create(Files.createDirectories(dir.resolve("iso2")))
            .shard(SnapshotFixture.RUNS, "{\"kind\":\"metadata\",\"records\":1}\n" + REPO_API).write();
        two.ingest(small, IngestOptions.defaults());
        assertEquals(1L, two.counts().get("repositories"));
        assertEquals(2L, one.counts().get("repositories"));
    }

    @Test
    void runtimeOpensOnlyProvisionedStateAndCannotRunDdl() throws Exception {
        assertThrows(CanonicalStoreException.class, () -> PostgresCanonicalStore.open(runtime, "never-provisioned"));
        try (Connection c = DriverManager.getConnection(runtime.jdbcUrl(), runtime.connectionProperties());
             Statement s = c.createStatement()) {
            assertThrows(SQLException.class, () -> s.execute("CREATE TABLE cao_runtime_ddl(x int)"));
            assertThrows(SQLException.class, () -> s.execute("DROP TABLE cao_canonical_runs"));
            assertThrows(SQLException.class, () -> s.execute("TRUNCATE cao_canonical_runs"));
        }
        assertEquals(0, scalar("SELECT count(*) FROM pg_tables WHERE tablename = 'cao_runtime_ddl'"));
    }

    @Test
    void databaseRejectsInconsistentStateDirectly() throws Exception {
        String namespace = freshNamespace();
        try (Connection c = DriverManager.getConnection(owner.jdbcUrl(), owner.connectionProperties());
             Statement s = c.createStatement()) {
            assertThrows(SQLException.class, () -> s.execute("UPDATE cao_canonical_state SET revision = 3 "
                + "WHERE namespace = '" + namespace + "'"));
            assertThrows(SQLException.class, () -> s.execute("INSERT INTO cao_canonical_repositories"
                + "(namespace, id, ordinal, record) VALUES ('" + namespace + "', 'x', 0, '[]')"));
        }
    }

    @Test
    void bootstrapIsRepeatableAndPreservesPublishedData() throws Exception {
        String namespace = freshNamespace();
        PostgresCanonicalStore store = store(namespace);
        store.ingest(snapshot("repeat"), IngestOptions.defaults());
        CanonicalBootstrap.provision(owner, namespace);
        assertEquals(1, store.state().revision());
        assertEquals(2L, store.counts().get("runs"));
    }
}
