package org.cao.backend.http;

import java.net.InetAddress;
import java.util.Map;

/** Environment-derived listener settings. Invalid or unsafe values fail startup. */
public record ServerConfig(String bind, int port, long shutdownGraceSeconds) {

    public static final String BIND = "CAO_BIND";
    public static final String PORT = "CAO_PORT";
    public static final String GRACE = "CAO_SHUTDOWN_GRACE_SECONDS";
    public static final String ALLOW_NON_LOOPBACK = "CAO_ALLOW_NON_LOOPBACK";

    public static ServerConfig fromEnvironment(Map<String, String> env) {
        String bind = env.getOrDefault(BIND, "127.0.0.1");
        if (!bind.matches("[0-9.]+") && !bind.contains(":")) {
            throw new IllegalArgumentException(BIND + " must be a numeric IP address");
        }
        InetAddress address;
        try {
            address = InetAddress.getByName(bind);
        } catch (java.net.UnknownHostException e) {
            throw new IllegalArgumentException(BIND + " is not a valid IP address", e);
        }
        if (!address.isLoopbackAddress() && !"true".equals(env.get(ALLOW_NON_LOOPBACK))) {
            throw new IllegalArgumentException(
                "non-loopback bind requires " + ALLOW_NON_LOOPBACK + "=true");
        }
        int port = parse(env.getOrDefault(PORT, "8080"), PORT, 0, 65535);
        long grace = parse(env.getOrDefault(GRACE, "10"), GRACE, 0, 300);
        return new ServerConfig(bind, port, grace);
    }

    private static int parse(String value, String name, int min, int max) {
        try {
            int parsed = Integer.parseInt(value);
            if (parsed < min || parsed > max) {
                throw new IllegalArgumentException(name + " out of range");
            }
            return parsed;
        } catch (NumberFormatException e) {
            throw new IllegalArgumentException(name + " must be an integer", e);
        }
    }
}
