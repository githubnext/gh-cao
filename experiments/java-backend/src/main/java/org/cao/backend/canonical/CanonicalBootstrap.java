package org.cao.backend.canonical;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.SQLException;
import java.sql.Statement;
import org.cao.backend.operational.PostgresEndpoint;

/**
 * Explicit schema-owner operation that creates the canonical tables and registers a namespace.
 * Runtime processes never call it: they open pre-provisioned state only, with the grants documented in
 * the README (SELECT/UPDATE on the state table; SELECT/INSERT/DELETE on the data tables).
 */
public final class CanonicalBootstrap {
    private static final int LOCK_A = 712083241;
    private static final int LOCK_B = 17486;

    private CanonicalBootstrap() { }

    public static void provision(PostgresEndpoint endpoint, String namespace) {
        PostgresCanonicalStore.requireNamespace(namespace);
        String ddl = loadSchema();
        try (Connection c = DriverManager.getConnection(endpoint.jdbcUrl(),
            endpoint.connectionProperties())) {
            c.setAutoCommit(false);
            try (PreparedStatement lock = c.prepareStatement("SELECT pg_advisory_xact_lock(?, ?)")) {
                lock.setInt(1, LOCK_A);
                lock.setInt(2, LOCK_B);
                lock.execute();
            }
            try (Statement s = c.createStatement()) {
                s.execute(ddl);
            }
            try (PreparedStatement insert = c.prepareStatement(
                "INSERT INTO cao_canonical_state(namespace) VALUES (?) ON CONFLICT DO NOTHING")) {
                insert.setString(1, namespace);
                insert.executeUpdate();
            }
            c.commit();
        } catch (SQLException e) {
            throw new CanonicalStoreException("canonical bootstrap failed (SQLSTATE " + e.getSQLState() + ")");
        }
    }

    private static String loadSchema() {
        try (InputStream in = CanonicalBootstrap.class.getResourceAsStream("schema.sql")) {
            if (in == null) {
                throw new CanonicalStoreException("canonical schema resource is missing");
            }
            return new String(in.readAllBytes(), StandardCharsets.UTF_8);
        } catch (IOException e) {
            throw new CanonicalStoreException("canonical schema resource unreadable");
        }
    }
}
