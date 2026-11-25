package org.cao.backend.http;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.TreeMap;
import org.cao.backend.policy.PolicyException;
import org.junit.jupiter.api.Test;

/** specs/server-cors.md §§2-4 and §6 conformance cases over real loopback sockets. */
class CorsTest {
    private static final String LISTED = "https://tools.example.com";

    record Reply(int status, Map<String, List<String>> headers, String body) {
        String first(String name) {
            List<String> values = headers.get(name.toLowerCase(Locale.ROOT));
            return values == null ? null : values.get(0);
        }

        List<String> all(String name) {
            return headers.getOrDefault(name.toLowerCase(Locale.ROOT), List.of());
        }

        boolean hasCorsHeader() {
            return headers.keySet().stream().anyMatch(k -> k.startsWith("access-control-"));
        }
    }

    /** Raw HTTP/1.1 so the Host header can be chosen freely. */
    private static Reply send(ApiServer server, String method, String path, String host, String... headers)
            throws Exception {
        StringBuilder request = new StringBuilder(method + " " + path + " HTTP/1.1\r\n");
        request.append("Host: ").append(host).append("\r\nConnection: close\r\n");
        for (int i = 0; i < headers.length; i += 2) {
            request.append(headers[i]).append(": ").append(headers[i + 1]).append("\r\n");
        }
        request.append("\r\n");
        try (Socket socket = new Socket(InetAddress.getByName("127.0.0.1"), server.port())) {
            socket.setSoTimeout(5000);
            socket.getOutputStream().write(request.toString().getBytes(StandardCharsets.UTF_8));
            socket.getOutputStream().flush();
            ByteArrayOutputStream all = new ByteArrayOutputStream();
            InputStream in = socket.getInputStream();
            byte[] buffer = new byte[4096];
            for (int n = in.read(buffer); n >= 0; n = in.read(buffer)) {
                all.write(buffer, 0, n);
            }
            String text = all.toString(StandardCharsets.UTF_8);
            int split = text.indexOf("\r\n\r\n");
            String[] lines = text.substring(0, split).split("\r\n");
            int status = Integer.parseInt(lines[0].split(" ")[1]);
            Map<String, List<String>> map = new TreeMap<>();
            for (int i = 1; i < lines.length; i++) {
                int colon = lines[i].indexOf(':');
                map.computeIfAbsent(lines[i].substring(0, colon).toLowerCase(Locale.ROOT), k -> new ArrayList<>())
                    .add(lines[i].substring(colon + 1).trim());
            }
            return new Reply(status, map, text.substring(split + 4));
        }
    }

    private static ApiServer start(CorsPolicy policy) throws Exception {
        ApiServer server = new ApiServer(new ServerConfig("127.0.0.1", 0, 1), ServiceState.unconfigured(), policy);
        server.start();
        return server;
    }

    private static String host(ApiServer s) {
        return "127.0.0.1:" + s.port();
    }

    private static CorsPolicy listed(Integer maxAge) {
        return CorsPolicy.of(List.of(LISTED), maxAge);
    }

    // --- §6.1 normalization ---

    @Test
    void normalizesCaseDefaultPortsTrailingSlashLoopbackAndDeduplicates() {
        CorsPolicy p = CorsPolicy.of(List.of(
            "https://Dashboard.Example.com", "https://dashboard.example.com:443/",
            "https://tools.example.com:8443", "http://localhost:5173", "http://[::1]:8080",
            "http://127.0.0.1:80", "HTTP://LOCALHOST:5173"), null);
        assertEquals(List.of("https://dashboard.example.com", "https://tools.example.com:8443",
            "http://localhost:5173", "http://[::1]:8080", "http://127.0.0.1"), p.allowedOrigins());
        assertEquals(600, p.maxAge());
    }

    // --- §6.2 / §3.3 rejection ---

    @Test
    void rejectsEveryInvalidForm() {
        for (String origin : new String[] {
            "*", "null", "", "https://*.example.com", "http://dashboard.example.com",
            "https://dashboard.example.com/path", "https://user@dashboard.example.com",
            "https://dashboard.example.com?x=1", "https://dashboard.example.com#x", "https://dashboard.example.com?",
            "ftp://dashboard.example.com", "dashboard.example.com", "https://bücher.example",
            "https://example.com.", "https://exa_mple.com", "https://a..example.com", "https://.example.com",
            "https://example.com:0", "https://example.com:65536", "https://example.com:99999999999",
            "http://10.0.0.1", "http://[2001:db8::1]", "http://999.0.0.1", "https://[zz::1]", " https://a.example"}) {
            assertThrows(PolicyException.class, () -> CorsPolicy.of(List.of(origin), null), origin);
        }
        assertThrows(PolicyException.class, () -> CorsPolicy.of(List.of("https://a.example"), 0));
        assertThrows(PolicyException.class, () -> CorsPolicy.of(List.of("https://a.example"), 86401));
        List<String> many = new ArrayList<>();
        for (int i = 0; i < 33; i++) {
            many.add("https://h" + i + ".example");
        }
        assertThrows(PolicyException.class, () -> CorsPolicy.of(many, null));
        assertEquals(32, CorsPolicy.of(many.subList(0, 32), 86400).allowedOrigins().size());
    }

    @Test
    void ipv6LiteralsNeverTriggerDnsLookup() {
        assertThrows(PolicyException.class, () -> CorsPolicy.of(List.of("http://[not.a.literal.example]"), null));
    }

    // --- §6.3 policy loader ---

    @Test
    void readsCorsFromCaoJsonAndDefaultsToSameOrigin() {
        String doc = """
            {"version":1,"control-plane":{"web":{"host":{"target":{"module":"generic"},
            "cors":{"allowed-origins":["https://Tools.example.com:443"],"max-age":120}}}}}""";
        CorsPolicy p = HostPolicy.loadCors(doc.getBytes(StandardCharsets.UTF_8));
        assertEquals(List.of(LISTED), p.allowedOrigins());
        assertEquals(120, p.maxAge());
        assertFalse(HostPolicy.loadCors("{\"version\":1,\"control-plane\":{}}".getBytes(StandardCharsets.UTF_8)).enabled());
        assertFalse(HostPolicy.loadCors(
            "{\"version\":1,\"control-plane\":{\"web\":{\"host\":{}}}}".getBytes(StandardCharsets.UTF_8)).enabled());
    }

    @Test
    void policyLoaderRejectsUnknownMembersCredentialsCaseDuplicatesAndBadDocuments() {
        for (String cors : new String[] {
            "{\"allow-credentials\":true}", "{\"allowed-origins\":[\"https://a.example\"],\"allow-credentials\":false}",
            "{\"allowed-origins\":[\"https://A.example\",\"https://a.example\"]}",
            "{\"allowed-origins\":\"https://a.example\"}", "{\"allowed-origins\":[1]}",
            "{\"max-age\":\"60\"}", "{\"max-age\":0}", "[]"}) {
            String doc = "{\"version\":1,\"control-plane\":{\"web\":{\"host\":{\"cors\":" + cors + "}}}}";
            assertThrows(PolicyException.class, () -> HostPolicy.loadCors(doc.getBytes(StandardCharsets.UTF_8)), cors);
        }
        for (String doc : new String[] {
            "{\"version\":2,\"control-plane\":{}}", "{\"version\":1}", "[]", "",
            "{\"version\":1,\"control-plane\":{\"web\":{\"host\":{\"cors\":{},\"cors\":{}}}}}",
            "{\"version\":1,\"control-plane\":{\"web\":{\"host\":{\"extra\":1}}}}",
            "{\"version\":1,\"control-plane\":{\"web\":{\"bogus\":1}}}",
            "{\"version\":1,\"control-plane\":{\"web\":{\"host\":{\"cors\":{\"allowed-origins\":[\"${{ secrets.X }}\"]}}}}}"}) {
            assertThrows(PolicyException.class, () -> HostPolicy.loadCors(doc.getBytes(StandardCharsets.UTF_8)), doc);
        }
        assertThrows(PolicyException.class, () -> HostPolicy.loadCors(new byte[] {(byte) 0xff, '{', '}'}));
    }

    @Test
    void mainReadsCorsOnlyFromPolicyDocumentNotEnvironmentOrigins() {
        assertFalse(Main.loadCors(Map.of("CAO_CORS_ALLOWED_ORIGINS", LISTED)).enabled());
        assertThrows(IllegalArgumentException.class, () -> Main.loadCors(Map.of("CAO_POLICY_PATH", "/nonexistent/cao.json")));
    }

    // --- §6.4 same-origin default ---

    @Test
    void sameOriginDefaultEmitsNoCorsHeadersButKeepsResourcePolicy() throws Exception {
        ApiServer s = start(CorsPolicy.sameOrigin());
        try {
            Reply get = send(s, "GET", "/api/health", host(s), "Origin", LISTED);
            assertFalse(get.hasCorsHeader());
            assertTrue(get.all("Vary").isEmpty());
            assertEquals("same-origin", get.first("Cross-Origin-Resource-Policy"));
            Reply pre = send(s, "OPTIONS", "/api/health", host(s),
                "Origin", LISTED, "Access-Control-Request-Method", "GET");
            assertEquals(405, pre.status());
            assertFalse(pre.hasCorsHeader());
        } finally {
            s.stop(0);
        }
    }

    // --- §6.5 / §6.6 preflight ---

    @Test
    void preflightForListedOriginAndGetIsAnswered204WithMaxAgeAndNoCredentials() throws Exception {
        ApiServer s = start(listed(120));
        try {
            for (String method : new String[] {"GET", "HEAD"}) {
                Reply r = send(s, "OPTIONS", "/api/anything", host(s), "Origin", LISTED,
                    "Access-Control-Request-Method", method, "Access-Control-Request-Headers", "traceparent");
                assertEquals(204, r.status());
                assertEquals(LISTED, r.first("Access-Control-Allow-Origin"));
                assertEquals("GET, HEAD", r.first("Access-Control-Allow-Methods"));
                assertEquals("Traceparent", r.first("Access-Control-Allow-Headers"));
                assertEquals("120", r.first("Access-Control-Max-Age"));
                assertNull(r.first("Access-Control-Allow-Credentials"));
                assertEquals("", r.body());
                assertEquals(List.of("Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"),
                    r.all("Vary"));
            }
        } finally {
            s.stop(0);
        }
    }

    @Test
    void preflightForOtherMethodsOmitsPermissionHeaders() throws Exception {
        ApiServer s = start(listed(null));
        try {
            for (String method : new String[] {"POST", "DELETE", "PUT", "PATCH", "get", "TRACE"}) {
                Reply r = send(s, "OPTIONS", "/api/health", host(s), "Origin", LISTED,
                    "Access-Control-Request-Method", method);
                assertEquals(204, r.status(), method);
                assertEquals(LISTED, r.first("Access-Control-Allow-Origin"));
                assertNull(r.first("Access-Control-Allow-Methods"), method);
                assertNull(r.first("Access-Control-Allow-Headers"), method);
                assertNull(r.first("Access-Control-Max-Age"), method);
            }
        } finally {
            s.stop(0);
        }
    }

    // --- §6.7 unlisted origins ---

    @Test
    void unlistedOriginsGetNoCorsHeadersButVaryOrigin() throws Exception {
        ApiServer s = start(listed(null));
        try {
            for (String origin : new String[] {"https://evil.example", "https://tools.example.com.evil.example",
                "https://TOOLS.example.com", "http://tools.example.com", "null", LISTED + "/"}) {
                Reply r = send(s, "GET", "/api/health", host(s), "Origin", origin);
                assertFalse(r.hasCorsHeader(), origin);
                assertEquals(List.of("Origin"), r.all("Vary"));
                Reply pre = send(s, "OPTIONS", "/api/health", host(s), "Origin", origin,
                    "Access-Control-Request-Method", "GET");
                assertFalse(pre.hasCorsHeader(), origin);
                assertEquals(405, pre.status(), origin);
            }
            Reply none = send(s, "GET", "/api/health", host(s));
            assertFalse(none.hasCorsHeader());
            assertEquals(List.of("Origin"), none.all("Vary"));
            Reply twice = send(s, "GET", "/api/health", host(s), "Origin", LISTED, "Origin", LISTED);
            assertFalse(twice.hasCorsHeader());
        } finally {
            s.stop(0);
        }
    }

    // --- §6.8 invalid host ---

    @Test
    void invalidHostGetsMisdirectedRejectionAndNoCorsHeaders() throws Exception {
        ApiServer s = start(listed(null));
        try {
            for (String bad : new String[] {"attacker.example", "attacker.example:" + s.port(),
                "127.0.0.1.attacker.example", "localhost.evil.example", "10.0.0.1", "[::2]", "127.0.0.1:abc",
                "127.0.0.1:0", "127.0.0.1:99999", "999.0.0.1", "[::1"}) {
                Reply pre = send(s, "OPTIONS", "/api/health", bad, "Origin", LISTED,
                    "Access-Control-Request-Method", "POST");
                assertEquals(421, pre.status(), bad);
                assertFalse(pre.hasCorsHeader(), bad);
                assertEquals(List.of("Origin"), pre.all("Vary"), bad);
                Reply get = send(s, "GET", "/api/health", bad, "Origin", LISTED);
                assertEquals(421, get.status(), bad);
                assertFalse(get.hasCorsHeader(), bad);
            }
            for (String good : new String[] {"localhost", "LOCALHOST:" + s.port(), "127.0.0.1", "[::1]",
                "[::1]:" + s.port(), "127.1.2.3"}) {
                assertEquals(204, send(s, "OPTIONS", "/api/health", good, "Origin", LISTED,
                    "Access-Control-Request-Method", "GET").status(), good);
            }
        } finally {
            s.stop(0);
        }
    }

    @Test
    void localProfileRejectsForeignHostEvenWithoutCorsPolicy() throws Exception {
        ApiServer s = start(CorsPolicy.sameOrigin());
        try {
            assertEquals(421, send(s, "GET", "/api/health", "rebind.example").status());
            assertEquals(503, send(s, "GET", "/api/health", host(s)).status());
        } finally {
            s.stop(0);
        }
    }

    // --- §4.5 actual requests ---

    @Test
    void actualRequestFromListedOriginIsReadableWithExactOriginAndNeverCredentialed() throws Exception {
        ApiServer s = start(listed(null));
        try {
            Reply ok = send(s, "GET", "/api/health", host(s), "Origin", LISTED);
            assertEquals(503, ok.status());
            assertEquals(LISTED, ok.first("Access-Control-Allow-Origin"));
            assertNull(ok.first("Access-Control-Allow-Credentials"));
            assertEquals(List.of("Origin"), ok.all("Vary"));
            assertTrue(ok.body().contains("unhealthy"));
            Reply missing = send(s, "GET", "/api/nope", host(s), "Origin", LISTED);
            assertEquals(404, missing.status());
            assertEquals(LISTED, missing.first("Access-Control-Allow-Origin"));
            Reply post = send(s, "POST", "/api/health", host(s), "Origin", LISTED);
            assertEquals(405, post.status());
            assertEquals(LISTED, post.first("Access-Control-Allow-Origin"));
            assertFalse(ok.all("Access-Control-Allow-Origin").contains("*"));
        } finally {
            s.stop(0);
        }
    }

    @Test
    void corsFailsClosedOutsideTheLoopbackProfile() {
        assertThrows(IllegalArgumentException.class,
            () -> new ApiServer(new ServerConfig("0.0.0.0", 0, 1), ServiceState.unconfigured(), listed(null)));
    }
}
