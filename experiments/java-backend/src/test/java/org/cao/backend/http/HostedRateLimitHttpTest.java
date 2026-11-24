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
import java.time.Duration;
import java.util.List;
import java.util.Map;
import org.cao.backend.operational.OperationalBootstrap;
import org.cao.backend.operational.OperationalStoreException;
import org.cao.backend.operational.PostgresOperationalStore;
import org.cao.backend.operational.TestPostgres;
import org.cao.backend.ratelimit.RateLimitConfig;
import org.junit.jupiter.api.Test;

/**
 * Hosted admission over real loopback sockets, with the real PostgreSQL limiter. The test client is
 * on 127.0.0.1, configured as the sole trusted proxy, and supplies forwarded host, protocol and
 * client address like an enterprise proxy would. It proves the local listener, not a deployment.
 */
class HostedRateLimitHttpTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final HttpClient CLIENT = HttpClient.newHttpClient();

    private static HttpResponse<String> call(ApiServer s, String method, String path, String client,
        String host, String proto) throws Exception {
        HttpRequest.Builder b = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + s.port() + path))
            .method(method, HttpRequest.BodyPublishers.noBody());
        if (client != null) {
            b.header("X-Forwarded-For", client);
        }
        if (host != null) {
            b.header("X-Forwarded-Host", host);
        }
        if (proto != null) {
            b.header("X-Forwarded-Proto", proto);
        }
        return CLIENT.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }

    @Test
    void probesAreMeteredPerClientAndFailClosedWhenTheDatabaseIsLost() throws Exception {
        try (TestPostgres pg = TestPostgres.start()) {
            OperationalBootstrap.provision(pg.endpoint(), "hosted-dashboard");
            PostgresOperationalStore store = PostgresOperationalStore.open(pg.endpoint(), "hosted-dashboard");
            ProxyPolicy proxy = new ProxyPolicy(List.of("cao.example.com"), true, true,
                List.of(ProxyPolicy.Cidr.parse("127.0.0.0/8")));
            RateLimitConfig limits = RateLimitConfig.defaults()
                .with(new RateLimitConfig.Policy("public", 3, Duration.ofHours(1)));
            ApiServer server = new ApiServer(new ServerConfig("127.0.0.1", 0, 1),
                new PostgresServiceState(store), RequestAdmission.hosted(proxy, store, limits));
            server.start();
            try {
                String client = "203.0.113.9";
                for (int i = 0; i < 3; i++) {
                    HttpResponse<String> r = call(server, "GET", "/api/readiness", client,
                        "cao.example.com", "https");
                    assertEquals(503, r.statusCode(), "not ready (no canonical data) but admitted: " + i);
                    assertEquals("3", r.headers().firstValue("RateLimit-Limit").orElseThrow());
                    assertEquals("3;w=3600", r.headers().firstValue("RateLimit-Policy").orElseThrow());
                    assertEquals(Integer.toString(2 - i),
                        r.headers().firstValue("RateLimit-Remaining").orElseThrow());
                    assertTrue(JSON.readTree(r.body()).has("ready"));
                }
                HttpResponse<String> limited = call(server, "GET", "/api/health", client,
                    "cao.example.com", "https");
                assertEquals(429, limited.statusCode());
                assertEquals("0", limited.headers().firstValue("RateLimit-Remaining").orElseThrow());
                assertTrue(Long.parseLong(limited.headers().firstValue("Retry-After").orElseThrow()) > 0);
                assertFalse(JSON.readTree(limited.body()).has("status"), "the handler must not run");

                HttpResponse<String> other = call(server, "GET", "/api/v1/health", "203.0.113.10",
                    "cao.example.com", "https");
                assertEquals(503, other.statusCode());
                assertEquals("2", other.headers().firstValue("RateLimit-Remaining").orElseThrow());

                JsonNode health = JSON.readTree(call(server, "GET", "/api/health", "203.0.113.11",
                    "cao.example.com", "https").body());
                JsonNode cap = health.get("operational").get("capabilities");
                assertEquals("deployment", cap.get("requestLimits").get("scope").asText());
                assertEquals("restart", cap.get("requestLimits").get("persistence").asText());
                assertEquals("unsupported", cap.get("sessions").get("scope").asText());
                assertEquals("unsupported", cap.get("cache").get("scope").asText());

                // A spoofed header from an untrusted peer is impossible here; an invalid host is rejected unmetered.
                assertEquals(421, call(server, "GET", "/api/health", client, "evil.example", "https")
                    .statusCode());
                assertEquals(421, call(server, "GET", "/api/health", client, "cao.example.com", "http")
                    .statusCode());
                assertEquals("0", call(server, "GET", "/api/health", client, "cao.example.com", "https")
                    .headers().firstValue("RateLimit-Remaining").orElseThrow(), "still exhausted");

                pg.stop();
                HttpResponse<String> down = call(server, "GET", "/api/health", "203.0.113.50",
                    "cao.example.com", "https");
                assertEquals(503, down.statusCode());
                assertEquals("request rate limiter is unavailable", JSON.readTree(down.body()).get("error").asText());
                assertTrue(down.headers().firstValue("RateLimit-Limit").isEmpty());
            } finally {
                server.stop(1);
                store.close();
            }
        }
    }

    @Test
    void localProfileLeavesProbesUnmetered() throws Exception {
        ApiServer server = new ApiServer(new ServerConfig("127.0.0.1", 0, 1), ServiceState.unconfigured());
        server.start();
        try {
            HttpResponse<String> r = call(server, "GET", "/api/health", null, null, null);
            assertEquals(503, r.statusCode());
            assertTrue(r.headers().firstValue("RateLimit-Limit").isEmpty());
            assertEquals("unsupported", JSON.readTree(r.body()).get("operational").get("capabilities")
                .get("requestLimits").get("scope").asText());
        } finally {
            server.stop(0);
        }
    }

    @Test
    void hostedConfigurationFailsClosed() throws Exception {
        assertFalse(HostedConfig.selected(Map.of()));
        assertThrows(IllegalArgumentException.class, () -> HostedConfig.admission(
            Map.of("CAO_HOSTED", "true", "CAO_ALLOWED_HOSTS", "cao.example.com"), null));
        try (TestPostgres pg = TestPostgres.start()) {
            OperationalBootstrap.provision(pg.endpoint(), "hosted-dashboard");
            try (PostgresOperationalStore store = PostgresOperationalStore.open(pg.endpoint(), "hosted-dashboard")) {
                assertThrows(IllegalArgumentException.class,
                    () -> HostedConfig.admission(Map.of("CAO_HOSTED", "true"), store));
                assertThrows(IllegalArgumentException.class, () -> HostedConfig.admission(
                    Map.of("CAO_ALLOWED_HOSTS", "cao.example.com", "CAO_TRUST_FORWARDED", "true"), store));
                assertThrows(IllegalArgumentException.class, () -> HostedConfig.admission(
                    Map.of("CAO_ALLOWED_HOSTS", "cao.example.com", "CAO_TRUST_FORWARDED", "maybe"), store));
                assertThrows(IllegalArgumentException.class, () -> HostedConfig.admission(
                    Map.of("CAO_ALLOWED_HOSTS", "cao.example.com", "CAO_TRUST_FORWARDED", "true",
                        "CAO_TRUSTED_PROXY_CIDRS", "10.0.0.0/99"), store));
                RequestAdmission ok = HostedConfig.admission(Map.of("CAO_ALLOWED_HOSTS", "cao.example.com",
                    "CAO_TRUST_FORWARDED", "true", "CAO_TRUSTED_PROXY_CIDRS", "10.0.0.0/8, fd00::/8"), store);
                assertTrue(ok.enforcing());
                assertTrue(ok.proxyPolicy().orElseThrow().requireHttps(), "HTTPS is required by default");
                assertEquals(2, ok.proxyPolicy().orElseThrow().trustedProxies().size());
            }
        }
        assertThrows(OperationalStoreException.class, () -> Main.openStore(Map.of("CAO_POSTGRES_URL",
            "postgres://u@db.example.com/cao")));
    }

    @Test
    void nonLoopbackListenerRequiresHostedMode() {
        ServerConfig open = ServerConfig.fromEnvironment(
            Map.of("CAO_BIND", "0.0.0.0", "CAO_ALLOW_NON_LOOPBACK", "true"));
        assertThrows(IllegalArgumentException.class,
            () -> HostedConfig.requireHostedForExposure(open, Map.of("CAO_ALLOW_NON_LOOPBACK", "true")));
        HostedConfig.requireHostedForExposure(open, Map.of("CAO_HOSTED", "true"));
        HostedConfig.requireHostedForExposure(ServerConfig.fromEnvironment(Map.of()), Map.of());
    }
}
