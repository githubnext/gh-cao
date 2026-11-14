package org.cao.backend.operational;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;

/**
 * Explicit schema-owner operation that creates the operational audit tables and registers a
 * namespace. Runtime processes never call it: they open pre-provisioned state only. Run it with the
 * owner's connection, never the runtime login.
 */
public final class OperationalBootstrap {
    private static final int LOCK_A = 712083241;
    private static final int LOCK_B = 17485;

    private OperationalBootstrap() { }

    public static void provision(PostgresEndpoint endpoint, String namespace) {
        PostgresOperationalStore.requireNamespace(namespace);
        String ddl = loadSchema();
        try (Connection c = DriverManager.getConnection(endpoint.jdbcUrl(),
            endpoint.connectionProperties())) {
            c.setAutoCommit(false);
            try (PreparedStatement lock = c.prepareStatement(
                "SELECT pg_advisory_xact_lock(?, ?)")) {
                lock.setInt(1, LOCK_A);
                lock.setInt(2, LOCK_B);
                lock.execute();
            }
            String schema;
            try (Statement s = c.createStatement();
                 ResultSet r = s.executeQuery("SELECT quote_ident(current_schema())")) {
                r.next();
                schema = r.getString(1);
            }
            try (Statement s = c.createStatement()) {
                s.execute(ddl.replace("{{schema}}", schema));
            }
            try (PreparedStatement insert = c.prepareStatement("INSERT INTO "
                + "cao_operational_namespaces(namespace, config) VALUES (?, ?) ON CONFLICT DO NOTHING")) {
                insert.setString(1, namespace);
                insert.setBytes(2, PostgresOperationalStore.DEFAULT_CONFIG);
                insert.executeUpdate();
            }
            c.commit();
        } catch (SQLException e) {
            throw new OperationalStoreException("operational bootstrap failed (SQLSTATE "
                + e.getSQLState() + ")");
        }
    }

    private static String loadSchema() {
        try (InputStream in = OperationalBootstrap.class.getResourceAsStream("schema.sql")) {
            if (in == null) {
                throw new OperationalStoreException("operational schema resource is missing");
            }
            return new String(in.readAllBytes(), StandardCharsets.UTF_8);
        } catch (IOException e) {
            throw new OperationalStoreException("operational schema resource unreadable", e);
        }
    }
}
