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
import java.time.Instant;
import java.util.Map;
import java.util.Optional;
import org.junit.jupiter.api.Test;

class ApiServerTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final HttpClient CLIENT = HttpClient.newHttpClient();

    private static ApiServer start(ServiceState state) throws Exception {
        ApiServer server = new ApiServer(new ServerConfig("127.0.0.1", 0, 1), state);
        server.start();
        return server;
    }

    private static HttpResponse<String> call(ApiServer s, String method, String path) throws Exception {
        HttpRequest req = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + s.port() + path))
            .method(method, HttpRequest.BodyPublishers.noBody()).build();
        return CLIENT.send(req, HttpResponse.BodyHandlers.ofString());
    }

    private static ServiceState ready() {
        return new ServiceState() {
            @Override
            public Operational operational() {
                return new Operational(true, true);
            }

            @Override
            public Data data() {
                return new Data(true, false, Optional.of(Instant.parse("2026-10-02T14:25:38Z")));
            }
        };
    }

    @Test
    void unconfiguredStateIsUnhealthyAndNotReady() throws Exception {
        ApiServer s = start(ServiceState.unconfigured());
        try {
            HttpResponse<String> h = call(s, "GET", "/api/health");
            assertEquals(503, h.statusCode());
            JsonNode hb = JSON.readTree(h.body());
            assertEquals("unhealthy", hb.get("status").asText());
            assertFalse(hb.get("redis").get("connected").asBoolean());
            assertFalse(hb.get("data").get("available").asBoolean());
            assertEquals("unsupported",
                hb.get("operational").get("capabilities").get("sessions").get("scope").asText());
            assertEquals("no-store", h.headers().firstValue("Cache-Control").orElseThrow());

            HttpResponse<String> r = call(s, "GET", "/api/readiness");
            assertEquals(503, r.statusCode());
            assertFalse(JSON.readTree(r.body()).get("ready").asBoolean());
        } finally {
            s.stop(0);
        }
    }

    @Test
    void connectedStateIsHealthyAndReady() throws Exception {
        ApiServer s = start(ready());
        try {
            HttpResponse<String> h = call(s, "GET", "/api/health");
            assertEquals(200, h.statusCode());
            JsonNode hb = JSON.readTree(h.body());
            assertEquals("healthy", hb.get("status").asText());
            assertEquals("2026-10-02T14:25:38Z", hb.get("data").get("evaluatedAt").asText());
            assertTrue(JSON.readTree(call(s, "GET", "/api/readiness").body()).get("ready").asBoolean());
        } finally {
            s.stop(0);
        }
    }

    @Test
    void connectedWithoutRecoveryIsNotReady() throws Exception {
        ServiceState state = new ServiceState() {
            @Override
            public Operational operational() {
                return new Operational(true, false);
            }

            @Override
            public Data data() {
                return new Data(true, false, Optional.empty());
            }
        };
        ApiServer s = start(state);
        try {
            assertEquals(503, call(s, "GET", "/api/readiness").statusCode());
        } finally {
            s.stop(0);
        }
    }

    @Test
    void unknownPathAndWrongMethodFailExplicitly() throws Exception {
        ApiServer s = start(ready());
        try {
            assertEquals(404, call(s, "GET", "/api/repositories").statusCode());
            HttpResponse<String> post = call(s, "POST", "/api/health");
            assertEquals(405, post.statusCode());
            assertEquals("GET", post.headers().firstValue("Allow").orElseThrow());
            assertTrue(JSON.readTree(post.body()).has("error"));
        } finally {
            s.stop(0);
        }
    }

    @Test
    void configDefaultsToLoopbackAndRejectsUnsafeValues() {
        ServerConfig c = ServerConfig.fromEnvironment(Map.of());
        assertEquals("127.0.0.1", c.bind());
        assertEquals(8080, c.port());
        assertThrows(IllegalArgumentException.class,
            () -> ServerConfig.fromEnvironment(Map.of("CAO_BIND", "0.0.0.0")));
        assertEquals("0.0.0.0", ServerConfig.fromEnvironment(
            Map.of("CAO_BIND", "0.0.0.0", "CAO_ALLOW_NON_LOOPBACK", "true")).bind());
        assertThrows(IllegalArgumentException.class,
            () -> ServerConfig.fromEnvironment(Map.of("CAO_BIND", "example.com")));
        assertThrows(IllegalArgumentException.class,
            () -> ServerConfig.fromEnvironment(Map.of("CAO_PORT", "70000")));
        assertThrows(IllegalArgumentException.class,
            () -> ServerConfig.fromEnvironment(Map.of("CAO_PORT", "x")));
    }

    @Test
    void stoppedServerRejectsConnections() throws Exception {
        ApiServer s = start(ready());
        int port = s.port();
        s.stop(0);
        HttpRequest req = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/api/health")).build();
        assertThrows(java.io.IOException.class, () -> CLIENT.send(req, HttpResponse.BodyHandlers.ofString()));
    }
}
