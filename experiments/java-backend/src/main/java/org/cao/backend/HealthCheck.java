package org.cao.backend;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;

/** Container HEALTHCHECK probe: exits 0 only when the local /api/health answers 200. */
public final class HealthCheck {
    private HealthCheck() {
    }

    public static void main(String[] args) throws Exception {
        String port = System.getenv().getOrDefault("CAO_PORT", "8080");
        HttpRequest request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/api/health"))
                .timeout(Duration.ofSeconds(3))
                .build();
        int status = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.discarding()).statusCode();
        System.exit(status == 200 ? 0 : 1);
    }
}
