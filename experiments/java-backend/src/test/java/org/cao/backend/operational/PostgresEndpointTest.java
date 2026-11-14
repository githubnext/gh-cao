package org.cao.backend.operational;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import org.junit.jupiter.api.Test;

class PostgresEndpointTest {

    @Test
    void loopbackMayUseAnySslMode() {
        PostgresEndpoint e = PostgresEndpoint.parse("postgres://u:p%40ss@127.0.0.1:5433/cao");
        assertEquals("jdbc:postgresql://127.0.0.1:5433/cao", e.jdbcUrl());
        assertEquals("p@ss", e.connectionProperties().getProperty("password"));
        assertEquals("postgres://127.0.0.1:5433/cao", e.toString());
        assertFalse(e.toString().contains("p@ss"));
        PostgresEndpoint.parse("postgresql://u@localhost/cao?sslmode=disable");
        PostgresEndpoint.parse("postgres://u@[::1]:5432/cao");
    }

    @Test
    void remoteHostsRequireVerifyFull() {
        assertThrows(OperationalStoreException.class,
            () -> PostgresEndpoint.parse("postgres://u@db.example.com/cao"));
        assertThrows(OperationalStoreException.class,
            () -> PostgresEndpoint.parse("postgres://u@db.example.com/cao?sslmode=require"));
        assertThrows(OperationalStoreException.class,
            () -> PostgresEndpoint.parse("postgres://u@db.example.com/cao?sslmode=verify-ca"));
        assertThrows(OperationalStoreException.class,
            () -> PostgresEndpoint.parse("postgres://u@10.0.0.5/cao?sslmode=prefer"));
        PostgresEndpoint ok = PostgresEndpoint.parse(
            "postgres://u@db.example.com/cao?sslmode=verify-full&sslrootcert=/etc/ca.pem");
        assertEquals("verify-full", ok.connectionProperties().getProperty("sslmode"));
    }

    @Test
    void rejectsUnsupportedOrAmbiguousInput() {
        for (String url : new String[] {"", "mysql://u@127.0.0.1/cao", "postgres:///cao",
            "postgres://u@127.0.0.1/", "postgres://u@127.0.0.1/cao?sslmode=bogus",
            "postgres://u@127.0.0.1/cao?host=/var/run", "postgres://u@127.0.0.1/cao?options=-c%20statement_timeout=0",
            "postgres://u@127.0.0.1,db.example.com/cao", "not a url"}) {
            assertThrows(OperationalStoreException.class, () -> PostgresEndpoint.parse(url), url);
        }
        assertThrows(OperationalStoreException.class, () -> PostgresEndpoint.parse(null));
    }

    @Test
    void searchPathOptionIsAllowedButNothingElse() {
        PostgresEndpoint e = PostgresEndpoint.parse(
            "postgres://u@127.0.0.1/cao?options=-c%20search_path%3Dcao");
        assertTrue(e.connectionProperties().getProperty("options").contains("search_path=cao"));
    }
}
