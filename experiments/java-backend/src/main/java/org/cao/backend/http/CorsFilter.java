package org.cao.backend.http;

import com.sun.net.httpserver.Filter;
import com.sun.net.httpserver.Headers;
import com.sun.net.httpserver.HttpExchange;
import java.io.IOException;
import java.util.List;

/**
 * Applies the reviewed CORS policy before access control (specs/server-cors.md §4). Preflights from
 * a listed origin and valid host are answered here without reaching the handler. Responses never
 * carry {@code Access-Control-Allow-Credentials} and never use a wildcard origin.
 */
final class CorsFilter extends Filter {
    private final CorsPolicy policy;
    private final String maxAge;

    CorsFilter(CorsPolicy policy) {
        this.policy = policy;
        this.maxAge = Integer.toString(policy.maxAge());
    }

    @Override
    public void doFilter(HttpExchange exchange, Chain chain) throws IOException {
        Headers out = exchange.getResponseHeaders();
        out.add("Vary", "Origin");
        List<String> origins = exchange.getRequestHeaders().get("Origin");
        boolean listed = origins != null && origins.size() == 1 && policy.allows(origins.get(0));
        if (!listed || !LocalHostRule.valid(exchange.getRequestHeaders().getFirst("Host"))) {
            chain.doFilter(exchange);
            return;
        }
        out.set("Access-Control-Allow-Origin", origins.get(0));
        String requested = exchange.getRequestHeaders().getFirst("Access-Control-Request-Method");
        if ("OPTIONS".equals(exchange.getRequestMethod()) && requested != null && !requested.isEmpty()) {
            out.add("Vary", "Access-Control-Request-Method");
            out.add("Vary", "Access-Control-Request-Headers");
            if ("GET".equals(requested) || "HEAD".equals(requested)) {
                out.set("Access-Control-Allow-Methods", "GET, HEAD");
                out.set("Access-Control-Allow-Headers", "Traceparent");
                out.set("Access-Control-Max-Age", maxAge);
            }
            exchange.sendResponseHeaders(204, -1);
            exchange.close();
            return;
        }
        chain.doFilter(exchange);
    }

    @Override
    public String description() {
        return "CORS policy (specs/server-cors.md section 4)";
    }
}
