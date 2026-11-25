package org.cao.backend.http;

import com.sun.net.httpserver.Filter;
import com.sun.net.httpserver.HttpExchange;
import java.io.IOException;

/** Always emits {@code Cross-Origin-Resource-Policy: same-origin} (specs/server-cors.md §2). */
final class ResourcePolicyFilter extends Filter {
    @Override
    public void doFilter(HttpExchange exchange, Chain chain) throws IOException {
        exchange.getResponseHeaders().set("Cross-Origin-Resource-Policy", "same-origin");
        chain.doFilter(exchange);
    }

    @Override
    public String description() {
        return "Cross-Origin-Resource-Policy";
    }
}
