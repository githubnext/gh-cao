package org.cao.backend.http;

import java.net.InetAddress;
import java.util.Locale;
import java.util.regex.Pattern;

/** Loopback request-host validation for the local profile (DNS-rebinding defence). */
final class LocalHostRule {
    private static final Pattern PORT = Pattern.compile("[0-9]{1,5}");
    private static final Pattern IPV4 = Pattern.compile("[0-9]{1,3}(?:\\.[0-9]{1,3}){3}");

    private LocalHostRule() { }

    /** Accepts {@code localhost} or a loopback IP literal, optionally with a numeric port. */
    static boolean valid(String hostHeader) {
        if (hostHeader == null || hostHeader.isEmpty()) {
            return false;
        }
        String host;
        String port = null;
        if (hostHeader.startsWith("[")) {
            int end = hostHeader.indexOf(']');
            if (end < 0) {
                return false;
            }
            host = hostHeader.substring(1, end);
            String rest = hostHeader.substring(end + 1);
            if (!rest.isEmpty()) {
                if (!rest.startsWith(":")) {
                    return false;
                }
                port = rest.substring(1);
            }
            return (port == null || validPort(port)) && CorsPolicy.ipv6(host)
                    .map(InetAddress::isLoopbackAddress).orElse(false);
        }
        int colon = hostHeader.indexOf(':');
        if (colon >= 0) {
            host = hostHeader.substring(0, colon);
            port = hostHeader.substring(colon + 1);
        } else {
            host = hostHeader;
        }
        if (port != null && !validPort(port)) {
            return false;
        }
        host = host.toLowerCase(Locale.ROOT);
        if ("localhost".equals(host)) {
            return true;
        }
        if (!IPV4.matcher(host).matches()) {
            return false;
        }
        String[] octets = host.split("\\.");
        for (String octet : octets) {
            if (Integer.parseInt(octet) > 255) {
                return false;
            }
        }
        return "127".equals(octets[0]);
    }

    private static boolean validPort(String port) {
        return PORT.matcher(port).matches() && Integer.parseInt(port) >= 1 && Integer.parseInt(port) <= 65535;
    }
}
