package org.cao.backend.http;

import java.util.Map;

/** Environment-derived listener configuration. Contains no secrets. */
public record ServerConfig(String host, int port, long probeTimeoutMillis, long shutdownGraceSeconds) {
    public ServerConfig {
        if (port < 0 || port > 65535) {
            throw new IllegalArgumentException("CAO_PORT must be 0..65535");
        }
        if (probeTimeoutMillis < 1 || shutdownGraceSeconds < 0) {
            throw new IllegalArgumentException("timeouts must be positive");
        }
    }

    /** Default host is numeric loopback; a container must opt in to 0.0.0.0 explicitly. */
    public static ServerConfig fromEnv(Map<String, String> env) {
        String host = env.getOrDefault("CAO_HOST", "127.0.0.1");
        if (!host.matches("[0-9.]+|[0-9a-fA-F:]+")) {
            throw new IllegalArgumentException("CAO_HOST must be a numeric address");
        }
        return new ServerConfig(
                host,
                parse(env, "CAO_PORT", 8080),
                parse(env, "CAO_PROBE_TIMEOUT_MS", 3000),
                parse(env, "CAO_SHUTDOWN_GRACE_SECONDS", 10));
    }

    private static int parse(Map<String, String> env, String key, int fallback) {
        String value = env.get(key);
        if (value == null || value.isBlank()) {
            return fallback;
        }
        try {
            return Integer.parseInt(value.trim());
        } catch (NumberFormatException e) {
            throw new IllegalArgumentException(key + " must be an integer");
        }
    }
}
