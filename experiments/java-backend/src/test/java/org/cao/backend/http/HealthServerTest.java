package org.cao.backend.http;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Instant;
import java.util.HashMap;
import java.util.Map;
import org.cao.backend.service.Capability;
import org.cao.backend.service.DataProbe;
import org.cao.backend.service.DataStatus;
import org.cao.backend.service.HealthService;
import org.cao.backend.service.OperationalProbe;
import org.cao.backend.service.OperationalStatus;
import org.cao.backend.service.Persistence;
import org.cao.backend.service.Scope;
import org.junit.jupiter.api.Test;

class HealthServerTest {
    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final HttpClient CLIENT = HttpClient.newHttpClient();

    private static OperationalStatus connected(boolean recovered) {
        Map<String, Capability> caps = new HashMap<>();
        OperationalStatus.CAPABILITY_NAMES.forEach(
                n -> caps.put(n, new Capability(Scope.PROCESS, Persistence.VOLATILE)));
        return new OperationalStatus(true, recovered, caps);
    }

    private static HealthServer start(OperationalProbe op, DataProbe data, long timeoutMs) throws IOException {
        HealthServer server = new HealthServer(
                new ServerConfig("127.0.0.1", 0, timeoutMs, 1), new HealthService(op, data, timeoutMs));
        server.start();
        return server;
    }

    private static HttpResponse<String> request(HealthServer server, String method, String path) throws Exception {
        HttpRequest request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + server.port() + path))
                .method(method, HttpRequest.BodyPublishers.noBody())
                .build();
        return CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
    }

    @Test
    void healthyWithAvailableDataMatchesSchemaShape() throws Exception {
        Instant at = Instant.parse("2026-10-02T14:25:38.957Z");
        try (HealthServer server = start(() -> connected(true), () -> new DataStatus(true, at), 1000)) {
            for (String path : new String[] {"/api/health", "/api/v1/health"}) {
                HttpResponse<String> response = request(server, "GET", path);
                assertEquals(200, response.statusCode());
                assertEquals("application/json", response.headers().firstValue("Content-Type").orElse(""));
                assertEquals("no-store", response.headers().firstValue("Cache-Control").orElse(""));
                JsonNode body = MAPPER.readTree(response.body());
                assertEquals("healthy", body.get("status").asText());
                assertTrue(body.get("redis").get("connected").asBoolean());
                assertTrue(body.get("data").get("available").asBoolean());
                assertFalse(body.get("data").get("rebuildRequired").asBoolean());
                assertEquals("2026-10-02T14:25:38.957Z", body.get("data").get("evaluatedAt").asText());
                assertEquals(8, body.get("operational").get("capabilities").size());
                assertEquals("process",
                        body.get("operational").get("capabilities").get("cache").get("scope").asText());
                assertFalse(body.has("revision"));
                assertFalse(body.has("counts"));
            }
        }
    }

    @Test
    void healthIsLiveBeforeFirstIngestionButReadinessIsNot() throws Exception {
        try (HealthServer server = start(() -> connected(true), DataStatus::empty, 1000)) {
            HttpResponse<String> health = request(server, "GET", "/api/health");
            assertEquals(200, health.statusCode());
            JsonNode data = MAPPER.readTree(health.body()).get("data");
            assertFalse(data.get("available").asBoolean());
            assertTrue(data.get("rebuildRequired").asBoolean());
            assertFalse(data.has("evaluatedAt"));

            HttpResponse<String> readiness = request(server, "GET", "/api/readiness");
            assertEquals(503, readiness.statusCode());
            assertFalse(MAPPER.readTree(readiness.body()).get("ready").asBoolean());
        }
    }

    @Test
    void readinessRequiresRecoveryAndData() throws Exception {
        DataProbe available = () -> new DataStatus(true, Instant.EPOCH);
        try (HealthServer server = start(() -> connected(false), available, 1000)) {
            assertEquals(503, request(server, "GET", "/api/readiness").statusCode());
        }
        try (HealthServer server = start(() -> connected(true), available, 1000)) {
            HttpResponse<String> response = request(server, "GET", "/api/readiness");
            assertEquals(200, response.statusCode());
            assertTrue(MAPPER.readTree(response.body()).get("ready").asBoolean());
        }
    }

    @Test
    void failingOrDisconnectedDependenciesAreUnhealthy() throws Exception {
        OperationalProbe broken = () -> {
            throw new IllegalStateException("boom: secret-host");
        };
        try (HealthServer server = start(broken, () -> new DataStatus(true, Instant.EPOCH), 1000)) {
            HttpResponse<String> response = request(server, "GET", "/api/health");
            assertEquals(503, response.statusCode());
            assertEquals("unhealthy", MAPPER.readTree(response.body()).get("status").asText());
            assertFalse(response.body().contains("secret-host"));
            assertEquals(503, request(server, "GET", "/api/readiness").statusCode());
        }
        DataProbe brokenData = () -> {
            throw new IllegalStateException("db down");
        };
        try (HealthServer server = start(() -> connected(true), brokenData, 1000)) {
            HttpResponse<String> response = request(server, "GET", "/api/health");
            assertEquals(503, response.statusCode());
            assertFalse(MAPPER.readTree(response.body()).get("data").get("available").asBoolean());
        }
        try (HealthServer server = start(OperationalStatus::unconfigured, DataStatus::empty, 1000)) {
            assertEquals(503, request(server, "GET", "/api/health").statusCode());
        }
    }

    @Test
    void slowProbeTimesOutAsUnhealthy() throws Exception {
        OperationalProbe slow = () -> {
            Thread.sleep(5000);
            return connected(true);
        };
        try (HealthServer server = start(slow, () -> new DataStatus(true, Instant.EPOCH), 100)) {
            assertEquals(503, request(server, "GET", "/api/health").statusCode());
        }
    }

    @Test
    void unknownPathsAndMethodsAreRejected() throws Exception {
        try (HealthServer server = start(() -> connected(true), () -> new DataStatus(true, Instant.EPOCH), 1000)) {
            assertEquals(404, request(server, "GET", "/api/v1/query").statusCode());
            assertEquals(404, request(server, "GET", "/").statusCode());
            HttpResponse<String> post = request(server, "POST", "/api/health");
            assertEquals(405, post.statusCode());
            assertEquals("GET, HEAD", post.headers().firstValue("Allow").orElse(""));
            HttpResponse<String> head = request(server, "HEAD", "/api/health");
            assertEquals(200, head.statusCode());
            assertEquals("", head.body());
        }
    }

    @Test
    void stoppedServerRefusesConnections() throws Exception {
        HealthServer server = start(() -> connected(true), () -> new DataStatus(true, Instant.EPOCH), 1000);
        int port = server.port();
        server.close();
        HttpRequest request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/api/health")).build();
        assertThrows(IOException.class, () -> CLIENT.send(request, HttpResponse.BodyHandlers.ofString()));
    }

    @Test
    void configDefaultsToLoopbackAndRejectsInvalidValues() {
        ServerConfig defaults = ServerConfig.fromEnv(Map.of());
        assertEquals("127.0.0.1", defaults.host());
        assertEquals(8080, defaults.port());
        assertEquals(9090, ServerConfig.fromEnv(Map.of("CAO_PORT", "9090")).port());
        assertThrows(IllegalArgumentException.class, () -> ServerConfig.fromEnv(Map.of("CAO_PORT", "x")));
        assertThrows(IllegalArgumentException.class, () -> ServerConfig.fromEnv(Map.of("CAO_PORT", "70000")));
        assertThrows(IllegalArgumentException.class, () -> ServerConfig.fromEnv(Map.of("CAO_HOST", "example.com")));
    }

    @Test
    void operationalStatusRequiresAllCapabilities() {
        assertThrows(IllegalArgumentException.class, () -> new OperationalStatus(true, true, Map.of()));
    }
}
