package org.cao.backend.http;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Serves {@code GET /api/health} and {@code GET /api/readiness} following
 * {@code server/spec/generated/openapi.json}. All other paths fail explicitly.
 */
public final class ApiServer {
    private static final ObjectMapper JSON = new ObjectMapper();

    private final HttpServer server;
    private final ServiceState state;
    private final AtomicBoolean accepting = new AtomicBoolean(true);

    public ApiServer(ServerConfig config, ServiceState state) throws IOException {
        this.state = state;
        this.server = HttpServer.create(new InetSocketAddress(config.bind(), config.port()), 0);
        server.setExecutor(Executors.newVirtualThreadPerTaskExecutor());
        server.createContext("/", this::route);
    }

    public void start() {
        server.start();
    }

    public int port() {
        return server.getAddress().getPort();
    }

    /** Fails new requests with 503, then drains in-flight requests for up to the grace period. */
    public void stop(long graceSeconds) {
        accepting.set(false);
        server.stop((int) graceSeconds);
    }

    private void route(HttpExchange exchange) throws IOException {
        try (exchange) {
            String path = exchange.getRequestURI().getPath();
            boolean health = "/api/health".equals(path);
            boolean readiness = "/api/readiness".equals(path);
            if (!health && !readiness) {
                send(exchange, 404, error("not found"));
            } else if (!"GET".equals(exchange.getRequestMethod())) {
                exchange.getResponseHeaders().set("Allow", "GET");
                send(exchange, 405, error("method not allowed"));
            } else if (!accepting.get()) {
                send(exchange, 503, error("unavailable"));
            } else if (health) {
                health(exchange);
            } else {
                readiness(exchange);
            }
        }
    }

    private void health(HttpExchange exchange) throws IOException {
        ServiceState.Operational op = state.operational();
        ServiceState.Data data = state.data();
        ObjectNode body = JSON.createObjectNode();
        boolean healthy = op.connected() && data.available() && !data.rebuildRequired();
        body.put("status", healthy ? "healthy" : "unhealthy");
        body.putObject("redis").put("connected", op.connected());
        operational(body, op);
        data(body, data);
        send(exchange, healthy ? 200 : 503, body);
    }

    private void readiness(HttpExchange exchange) throws IOException {
        ServiceState.Operational op = state.operational();
        ServiceState.Data data = state.data();
        ObjectNode body = JSON.createObjectNode();
        boolean ready = op.connected() && op.recoveryReady() && data.available();
        body.put("ready", ready);
        body.putObject("redis").put("connected", op.connected());
        operational(body, op);
        data(body, data);
        send(exchange, ready ? 200 : 503, body);
    }

    /** Capabilities are all unsupported: no operational adapter exists, so none is claimed. */
    private static void operational(ObjectNode body, ServiceState.Operational op) {
        ObjectNode node = body.putObject("operational");
        node.put("connected", op.connected());
        node.put("recoveryReady", op.recoveryReady());
        ObjectNode caps = node.putObject("capabilities");
        for (String name : new String[] {"cache", "requestLimits", "sessions", "revocations",
            "githubQuota", "collection", "coordination", "diagnostics"}) {
            ObjectNode cap = caps.putObject(name);
            cap.put("scope", "unsupported");
            cap.put("persistence", "volatile");
        }
    }

    private static void data(ObjectNode body, ServiceState.Data data) {
        ObjectNode node = body.putObject("data");
        node.put("available", data.available());
        node.put("rebuildRequired", data.rebuildRequired());
        data.evaluatedAt().ifPresent(t -> node.put("evaluatedAt", t.toString()));
    }

    private static ObjectNode error(String message) {
        return JSON.createObjectNode().put("error", message);
    }

    private static void send(HttpExchange exchange, int status, ObjectNode body) throws IOException {
        byte[] bytes = JSON.writeValueAsString(body).getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.getResponseHeaders().set("Cache-Control", "no-store");
        exchange.sendResponseHeaders(status, bytes.length);
        try (OutputStream out = exchange.getResponseBody()) {
            out.write(bytes);
        }
    }
}
