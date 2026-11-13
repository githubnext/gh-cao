package org.cao.backend.http;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.util.Map;
import org.cao.backend.operational.OperationalBootstrap;
import org.cao.backend.operational.OperationalStoreException;
import org.cao.backend.operational.PostgresEndpoint;
import org.cao.backend.operational.PostgresOperationalStore;
import org.cao.backend.operational.TestPostgres;
import org.junit.jupiter.api.Test;

/** End-to-end over loopback sockets and a real disposable PostgreSQL instance. */
class PostgresServiceStateTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final HttpClient CLIENT = HttpClient.newHttpClient();

    private static JsonNode get(ApiServer server, String path, int expectedStatus) throws Exception {
        HttpResponse<String> response = CLIENT.send(HttpRequest.newBuilder(
            URI.create("http://127.0.0.1:" + server.port() + path)).GET().build(),
            HttpResponse.BodyHandlers.ofString());
        assertEquals(expectedStatus, response.statusCode(), response.body());
        return JSON.readTree(response.body());
    }

    @Test
    void readinessReflectsRealConnectivityAndStaysNotReadyWithoutCanonicalData() throws Exception {
        try (TestPostgres pg = TestPostgres.start()) {
            PostgresEndpoint endpoint = pg.endpoint();
            OperationalBootstrap.provision(endpoint, "hosted-dashboard");
            PostgresOperationalStore store = PostgresOperationalStore.open(endpoint, "hosted-dashboard");
            ApiServer server = new ApiServer(new ServerConfig("127.0.0.1", 0, 1),
                new PostgresServiceState(store));
            server.start();
            try {
                JsonNode up = get(server, "/api/readiness", 503);
                assertFalse(up.get("ready").asBoolean());
                assertTrue(up.get("operational").get("connected").asBoolean());
                assertFalse(up.get("data").get("available").asBoolean());
                assertEquals("unhealthy", get(server, "/api/health", 503).get("status").asText());

                pg.stop();
                JsonNode down = get(server, "/api/readiness", 503);
                assertFalse(down.get("operational").get("connected").asBoolean());
                assertFalse(down.get("operational").get("recoveryReady").asBoolean());
            } finally {
                server.stop(1);
                store.close();
            }
        }
    }

    @Test
    void startupFailsClosedForUnsafeUnprovisionedOrMissingStore() throws Exception {
        assertEquals(null, Main.openStore(Map.of()));
        assertThrows(OperationalStoreException.class,
            () -> Main.openStore(Map.of("CAO_POSTGRES_URL", "postgres://u@db.example.com/cao")));
        try (TestPostgres pg = TestPostgres.start()) {
            Map<String, String> env = Map.of("CAO_POSTGRES_URL", pg.endpointUrl(),
                "CAO_OPERATIONAL_NAMESPACE", "missing");
            assertThrows(OperationalStoreException.class, () -> Main.openStore(env));
            OperationalBootstrap.provision(pg.endpoint(), "canonical-ns");
            // Operational namespace defaults to the canonical namespace when not overridden.
            try (PostgresOperationalStore store = Main.openStore(Map.of("CAO_POSTGRES_URL",
                pg.endpointUrl(), "CAO_DATABASE_NAMESPACE", "canonical-ns"))) {
                assertEquals("canonical-ns", store.namespace());
            }
        }
    }
}
