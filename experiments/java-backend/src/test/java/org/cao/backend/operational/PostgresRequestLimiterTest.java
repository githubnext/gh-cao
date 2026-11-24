package org.cao.backend.operational;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import org.cao.backend.ratelimit.RateLimitResult;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

/**
 * The PostgreSQL {@code RequestLimiter} against a real disposable loopback PostgreSQL, using the
 * least-privilege runtime role of {@code server/postgres/operational-grants.sql} for records.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class PostgresRequestLimiterTest {
    private static final Duration MINUTE = Duration.ofMinutes(1);
    private static final Duration DEADLINE = Duration.ofSeconds(5);

    private TestPostgres pg;
    private PostgresEndpoint owner;
    private PostgresEndpoint runtime;

    @BeforeAll
    void startDatabase() throws Exception {
        pg = TestPostgres.start();
        owner = PostgresEndpoint.parse(pg.url(TestPostgres.OWNER, "postgres"));
        try (Connection c = connect(owner); Statement s = c.createStatement()) {
            s.execute("CREATE ROLE cao_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE");
            s.execute("REVOKE CREATE ON SCHEMA public FROM PUBLIC, cao_runtime");
        }
        runtime = PostgresEndpoint.parse(pg.url(TestPostgres.RUNTIME, "postgres"));
        OperationalBootstrap.provision(owner, "limits");
        try (Connection c = connect(owner); Statement s = c.createStatement()) {
            s.execute("GRANT SELECT ON cao_operational_namespaces TO cao_runtime");
            s.execute("GRANT UPDATE (protected_bytes, protected_entries) "
                + "ON cao_operational_namespaces TO cao_runtime");
            s.execute("GRANT SELECT, INSERT, UPDATE, DELETE ON cao_operational_records TO cao_runtime");
            s.execute("GRANT USAGE, SELECT ON cao_operational_records_position_seq TO cao_runtime");
        }
    }

    @AfterAll
    void stopDatabase() throws Exception {
        pg.close();
    }

    private static Connection connect(PostgresEndpoint endpoint) throws SQLException {
        return DriverManager.getConnection(endpoint.jdbcUrl(), endpoint.connectionProperties());
    }

    private PostgresOperationalStore store(String namespace) {
        return PostgresOperationalStore.open(runtime, namespace);
    }

    private String scalar(String sql, String... args) throws SQLException {
        try (Connection c = connect(owner); PreparedStatement s = c.prepareStatement(sql)) {
            for (int i = 0; i < args.length; i++) {
                s.setString(i + 1, args[i]);
            }
            try (ResultSet r = s.executeQuery()) {
                r.next();
                return r.getString(1);
            }
        }
    }

    @Test
    void capacityIsSpentOneTokenAtATimeThenDeniedWithPositiveRetry() {
        try (PostgresOperationalStore s = store("limits")) {
            for (int i = 0; i < 3; i++) {
                RateLimitResult r = s.take("seq", 3, 1, MINUTE, DEADLINE);
                assertTrue(r.allowed(), "request " + i);
                assertEquals(2 - i, r.remaining(), "remaining after request " + i);
            }
            RateLimitResult denied = s.take("seq", 3, 1, MINUTE, DEADLINE);
            assertFalse(denied.allowed());
            assertTrue(denied.retryAfter().toMillis() > 0);
            assertTrue(denied.resetAfter().compareTo(MINUTE) <= 0);
        }
    }

    @Test
    void concurrentRequestsAreAtomicAndNeverExceedCapacity() throws Exception {
        int capacity = 20;
        try (PostgresOperationalStore s = store("limits");
             ExecutorService pool = Executors.newFixedThreadPool(8)) {
            List<Callable<Boolean>> calls = new ArrayList<>();
            for (int i = 0; i < 60; i++) {
                calls.add(() -> s.take("race", capacity, 1, Duration.ofHours(1), DEADLINE).allowed());
            }
            int allowed = 0;
            for (Future<Boolean> f : pool.invokeAll(calls)) {
                allowed += f.get() ? 1 : 0;
            }
            // One hour refills 20 tokens, so a 60 request burst within moments admits exactly 20.
            assertEquals(capacity, allowed);
        }
    }

    @Test
    void namespacesAreIsolated() throws Exception {
        OperationalBootstrap.provision(owner, "limits-other");
        try (PostgresOperationalStore a = store("limits"); PostgresOperationalStore b = store("limits-other")) {
            assertTrue(a.take("iso", 1, 1, Duration.ofHours(1), DEADLINE).allowed());
            assertFalse(a.take("iso", 1, 1, Duration.ofHours(1), DEADLINE).allowed());
            assertTrue(b.take("iso", 1, 1, Duration.ofHours(1), DEADLINE).allowed());
        }
    }

    @Test
    void deniedMultiTokenChargeDrainsTheStoredBucket() {
        try (PostgresOperationalStore s = store("limits")) {
            assertTrue(s.take("drain", 10, 1, Duration.ofHours(1), DEADLINE).allowed());
            RateLimitResult denied = s.take("drain", 10, 10, Duration.ofHours(1), DEADLINE);
            assertFalse(denied.allowed());
            assertEquals(0, denied.remaining());
            assertFalse(s.take("drain", 10, 10, Duration.ofHours(1), DEADLINE).allowed());
        }
    }

    @Test
    void storedRowIsTheGoCompatibleRecordWithTwoPeriodExpiryAndCapacityCharge() throws Exception {
        try (PostgresOperationalStore s = store("limits")) {
            s.take("shape", 5, 1, MINUTE, DEADLINE);
        }
        String value = scalar("SELECT convert_from(value, 'UTF8') FROM cao_operational_records "
            + "WHERE namespace = 'limits' AND kind = 'limiter' AND name = 'shape' AND field = ''");
        assertTrue(value.matches("\\{\"Tokens\":4(\\.\\d+)?,\"At\":\"[0-9T:.\\-]+Z\"\\}"), value);
        long expirySeconds = Long.parseLong(scalar("SELECT floor(extract(epoch FROM (expires_at - "
            + "clock_timestamp())))::text FROM cao_operational_records WHERE namespace = 'limits' "
            + "AND name = 'shape'"));
        assertTrue(expirySeconds >= 118 && expirySeconds <= 120, "expiry in seconds: " + expirySeconds);
        long charged = Long.parseLong(scalar("SELECT sum(octet_length(value) + 128 + octet_length(kind) "
            + "+ octet_length(name) + octet_length(field)) FROM cao_operational_records "
            + "WHERE namespace = 'limits' AND kind = 'limiter'"));
        assertEquals(charged, Long.parseLong(scalar(
            "SELECT protected_bytes FROM cao_operational_namespaces WHERE namespace = 'limits'")));
    }

    @Test
    void javaContinuesABucketWrittenByGoAndRejectsCorruptRows() throws Exception {
        // Exactly what Go's json.Marshal(tokenBucket{Tokens: 0, At: now}) stores.
        try (PostgresOperationalStore s = store("limits"); Connection c = connect(owner)) {
            String at = scalar("SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC', "
                + "'YYYY-MM-DD\"T\"HH24:MI:SS.MS\"Z\"')");
            insertRaw(c, "gobucket", ("{\"Tokens\":0,\"At\":\"" + at + "\"}").getBytes(StandardCharsets.UTF_8));
            RateLimitResult r = s.take("gobucket", 5, 1, Duration.ofHours(1), DEADLINE);
            assertFalse(r.allowed(), "a Go-exhausted bucket must stay exhausted in Java");

            insertRaw(c, "corrupt", "not json".getBytes(StandardCharsets.UTF_8));
            assertThrows(OperationalStoreException.class,
                () -> s.take("corrupt", 5, 1, MINUTE, DEADLINE));
            insertRaw(c, "negative", ("{\"Tokens\":-4,\"At\":\"" + at + "\"}").getBytes(StandardCharsets.UTF_8));
            assertThrows(OperationalStoreException.class,
                () -> s.take("negative", 5, 1, MINUTE, DEADLINE));
        }
    }

    private static void insertRaw(Connection c, String name, byte[] value) throws SQLException {
        try (PreparedStatement s = c.prepareStatement("INSERT INTO cao_operational_records"
            + "(namespace, kind, name, field, value, expires_at) VALUES ('limits', 'limiter', ?, '', ?, "
            + "now() + interval '1 hour') ON CONFLICT (namespace, kind, name, field) DO UPDATE "
            + "SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at")) {
            s.setString(1, name);
            s.setBytes(2, value);
            s.executeUpdate();
        }
    }

    @Test
    void invalidArgumentsFailBeforeTouchingStorage() {
        try (PostgresOperationalStore s = store("limits")) {
            assertThrows(IllegalArgumentException.class, () -> s.take("", 5, 1, MINUTE, DEADLINE));
            assertThrows(IllegalArgumentException.class, () -> s.take("k", 5, 6, MINUTE, DEADLINE));
            assertThrows(IllegalArgumentException.class, () -> s.take("k", 5, 1, Duration.ZERO, DEADLINE));
        }
    }

    @Test
    void expiredBucketsAreReclaimedAndReleaseTheirCapacityChargeButActiveOnesAreKept() throws Exception {
        try (PostgresOperationalStore s = store("limits")) {
            s.take("expire-me", 5, 1, MINUTE, DEADLINE);
            s.take("keep-me", 5, 1, MINUTE, DEADLINE);
            try (Connection c = connect(owner); Statement st = c.createStatement()) {
                st.execute("UPDATE cao_operational_records SET expires_at = now() - interval '1 second' "
                    + "WHERE namespace = 'limits' AND name = 'expire-me'");
            }
            String before = scalar("SELECT protected_entries FROM cao_operational_namespaces "
                + "WHERE namespace = 'limits'");
            s.take("brand-new", 5, 1, MINUTE, DEADLINE);
            assertEquals("0", scalar("SELECT count(*)::text FROM cao_operational_records "
                + "WHERE namespace = 'limits' AND name = 'expire-me'"));
            assertEquals("1", scalar("SELECT count(*)::text FROM cao_operational_records "
                + "WHERE namespace = 'limits' AND name = 'keep-me'"));
            // one removed, one added
            assertEquals(before, scalar("SELECT protected_entries FROM cao_operational_namespaces "
                + "WHERE namespace = 'limits'"));
            // an expired bucket restarts full rather than inheriting stale state
            try (Connection c = connect(owner); Statement st = c.createStatement()) {
                st.execute("UPDATE cao_operational_records SET expires_at = now() - interval '1 second' "
                    + "WHERE namespace = 'limits' AND name = 'keep-me'");
            }
            assertEquals(4, s.take("keep-me", 5, 1, MINUTE, DEADLINE).remaining());
        }
    }

    @Test
    void protectedCapacityExhaustionFailsClosedWithoutEvictingActiveBuckets() throws Exception {
        OperationalBootstrap.provision(owner, "limits-full");
        try (PostgresOperationalStore s = store("limits-full")) {
            assertTrue(s.take("first", 1, 1, Duration.ofHours(1), DEADLINE).allowed());
            try (Connection c = connect(owner); Statement st = c.createStatement()) {
                st.execute("UPDATE cao_operational_namespaces SET protected_entries = 200000 "
                    + "WHERE namespace = 'limits-full'");
            }
            assertThrows(OperationalStoreException.class,
                () -> s.take("second", 1, 1, Duration.ofHours(1), DEADLINE));
            // The existing active bucket was not evicted to make room: it is still exhausted.
            assertFalse(s.take("first", 1, 1, Duration.ofHours(1), DEADLINE).allowed());
        }
    }

    @Test
    void runtimeRoleCannotRunDdlOrTouchOtherTables() throws Exception {
        try (Connection c = connect(runtime); Statement s = c.createStatement()) {
            assertThrows(SQLException.class, () -> s.execute("DROP TABLE cao_operational_records"));
            assertThrows(SQLException.class, () -> s.execute("DELETE FROM cao_operational_namespaces"));
        }
    }

    @Test
    void lostDatabaseFailsClosedInsteadOfAllowing() throws Exception {
        try (TestPostgres own = TestPostgres.start()) {
            PostgresEndpoint endpoint = own.endpoint();
            OperationalBootstrap.provision(endpoint, "gone");
            try (PostgresOperationalStore s = PostgresOperationalStore.open(endpoint, "gone")) {
                assertTrue(s.take("up", 5, 1, MINUTE, DEADLINE).allowed());
                own.stop();
                assertThrows(OperationalStoreException.class, () -> s.take("up", 5, 1, MINUTE, DEADLINE));
            }
        }
    }
}
