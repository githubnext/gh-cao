package org.cao.backend.http;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.time.format.DateTimeFormatter;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;
import org.cao.backend.service.Capability;
import org.cao.backend.service.HealthReport;
import org.cao.backend.service.HealthService;
import org.cao.backend.service.OperationalStatus;

/**
 * Public health surface: {@code /api/health}, {@code /api/v1/health} (OpenAPI Health_health) and
 * {@code /api/readiness} (implemented by the Go server, absent from OpenAPI). Every other path is 404.
 * Authorized detail fields (revision, counts) are never emitted because authentication is not implemented.
 */
public final class HealthServer implements AutoCloseable {
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final HttpServer server;
    private final HealthService health;
    private final AtomicBoolean serving = new AtomicBoolean(false);
    private final long graceSeconds;

    public HealthServer(ServerConfig config, HealthService health) throws IOException {
        this.health = health;
        this.graceSeconds = config.shutdownGraceSeconds();
        this.server = HttpServer.create(new InetSocketAddress(config.host(), config.port()), 0);
        this.server.setExecutor(Executors.newVirtualThreadPerTaskExecutor());
        this.server.createContext("/", this::dispatch);
    }

    public void start() {
        server.start();
        serving.set(true);
    }

    public int port() {
        return server.getAddress().getPort();
    }

    /** Stops admitting work (503), drains in-flight exchanges up to the grace period, then closes. */
    @Override
    public void close() {
        serving.set(false);
        server.stop((int) graceSeconds);
        health.close();
    }

    private void dispatch(HttpExchange exchange) throws IOException {
        try (exchange) {
            String path = exchange.getRequestURI().getPath();
            boolean healthPath = path.equals("/api/health") || path.equals("/api/v1/health");
            boolean readinessPath = path.equals("/api/readiness");
            if (!healthPath && !readinessPath) {
                send(exchange, 404, error("not found"));
                return;
            }
            String method = exchange.getRequestMethod();
            if (!method.equals("GET") && !method.equals("HEAD")) {
                exchange.getResponseHeaders().set("Allow", "GET, HEAD");
                send(exchange, 405, error("method not allowed"));
                return;
            }
            if (!serving.get()) {
                send(exchange, 503, error("service unavailable"));
                return;
            }
            HealthReport report = health.evaluate();
            if (healthPath) {
                send(exchange, report.healthy() ? 200 : 503, healthBody(report));
            } else {
                send(exchange, report.ready() ? 200 : 503, readinessBody(report));
            }
        } catch (RuntimeException e) {
            // Never leak internals; the exchange may already be partially written.
            exchange.close();
        }
    }

    private static ObjectNode error(String message) {
        return MAPPER.createObjectNode().put("error", message);
    }

    private static ObjectNode healthBody(HealthReport report) {
        ObjectNode body = MAPPER.createObjectNode();
        body.put("status", report.healthy() ? "healthy" : "unhealthy");
        addCommon(body, report);
        return body;
    }

    private static ObjectNode readinessBody(HealthReport report) {
        ObjectNode body = MAPPER.createObjectNode();
        body.put("ready", report.ready());
        addCommon(body, report);
        return body;
    }

    private static void addCommon(ObjectNode body, HealthReport report) {
        OperationalStatus operational = report.operational();
        body.putObject("redis").put("connected", operational.connected());
        ObjectNode op = body.putObject("operational");
        op.put("connected", operational.connected());
        op.put("recoveryReady", operational.recoveryReady());
        ObjectNode caps = op.putObject("capabilities");
        for (String name : OperationalStatus.CAPABILITY_NAMES) {
            Capability capability = operational.capabilities().get(name);
            caps.putObject(name)
                    .put("scope", capability.scope().wire())
                    .put("persistence", capability.persistence().wire());
        }
        ObjectNode data = body.putObject("data");
        data.put("available", report.data().available());
        data.put("rebuildRequired", !report.data().available());
        if (report.data().available() && report.data().evaluatedAt() != null) {
            data.put("evaluatedAt", DateTimeFormatter.ISO_INSTANT.format(report.data().evaluatedAt()));
        }
    }

    private static void send(HttpExchange exchange, int status, ObjectNode body) throws IOException {
        byte[] bytes = MAPPER.writeValueAsBytes(body);
        var headers = exchange.getResponseHeaders();
        headers.set("Content-Type", "application/json");
        headers.set("Cache-Control", "no-store");
        headers.set("X-Content-Type-Options", "nosniff");
        headers.set("X-Frame-Options", "DENY");
        headers.set("Referrer-Policy", "same-origin");
        headers.set("Permissions-Policy", "camera=(), microphone=(), geolocation=()");
        headers.set("Cross-Origin-Resource-Policy", "same-origin");
        boolean head = exchange.getRequestMethod().equals("HEAD");
        exchange.sendResponseHeaders(status, head ? -1 : bytes.length);
        if (!head) {
            try (OutputStream out = exchange.getResponseBody()) {
                out.write(bytes);
            }
        }
    }
}
