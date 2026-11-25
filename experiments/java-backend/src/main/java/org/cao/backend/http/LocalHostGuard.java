package org.cao.backend.http;

import com.sun.net.httpserver.Filter;
import com.sun.net.httpserver.HttpExchange;
import java.io.IOException;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;

/** Rejects requests whose Host is not loopback with 421 in the local profile. */
final class LocalHostGuard extends Filter {
    private static final byte[] BODY = "{\"error\":\"invalid request host\"}".getBytes(StandardCharsets.UTF_8);

    @Override
    public void doFilter(HttpExchange exchange, Chain chain) throws IOException {
        if (LocalHostRule.valid(exchange.getRequestHeaders().getFirst("Host"))) {
            chain.doFilter(exchange);
            return;
        }
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.getResponseHeaders().set("Cache-Control", "no-store");
        exchange.sendResponseHeaders(421, BODY.length);
        try (OutputStream out = exchange.getResponseBody()) {
            out.write(BODY);
        }
    }

    @Override
    public String description() {
        return "local profile request host validation";
    }
}
