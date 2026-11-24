package org.cao.backend.http;

import java.net.InetAddress;
import java.net.UnknownHostException;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.regex.Pattern;

/**
 * Parses numeric IP literals without ever consulting DNS. {@code InetAddress.getByName} can resolve
 * names for malformed input, so client-supplied forwarding values are parsed here instead.
 */
final class IpLiteral {
    private static final Pattern DECIMAL = Pattern.compile("[0-9]{1,3}");
    private static final Pattern PORT = Pattern.compile("[0-9]{1,5}");
    private static final Pattern HEX = Pattern.compile("[0-9a-fA-F]{1,4}");

    private IpLiteral() { }

    /**
     * Accepts {@code v4}, {@code v4:port}, {@code v6}, {@code [v6]} and {@code [v6]:port}.
     * Anything else (names, {@code unknown}, obfuscated {@code _x} identifiers, zones) is empty.
     */
    static Optional<InetAddress> parseWithOptionalPort(String raw) {
        String value = raw.strip();
        if (value.startsWith("[")) {
            int close = value.indexOf(']');
            if (close < 0) {
                return Optional.empty();
            }
            String rest = value.substring(close + 1);
            if (!rest.isEmpty() && !validPort(rest)) {
                return Optional.empty();
            }
            return v6(value.substring(1, close));
        }
        int colons = (int) value.chars().filter(c -> c == ':').count();
        if (colons == 0) {
            return v4(value);
        }
        if (colons == 1) {
            int colon = value.indexOf(':');
            return validPort(value.substring(colon)) ? v4(value.substring(0, colon)) : Optional.empty();
        }
        return v6(value);
    }

    private static boolean validPort(String withColon) {
        if (!withColon.startsWith(":") || !PORT.matcher(withColon.substring(1)).matches()) {
            return false;
        }
        return Integer.parseInt(withColon.substring(1)) <= 65535;
    }

    static Optional<InetAddress> v4(String text) {
        byte[] bytes = ipv4Bytes(text);
        return bytes == null ? Optional.empty() : address(bytes);
    }

    private static byte[] ipv4Bytes(String text) {
        String[] parts = text.split("\\.", -1);
        if (parts.length != 4) {
            return null;
        }
        byte[] out = new byte[4];
        for (int i = 0; i < 4; i++) {
            if (!DECIMAL.matcher(parts[i]).matches() || parts[i].length() > 1 && parts[i].charAt(0) == '0') {
                return null;
            }
            int octet = Integer.parseInt(parts[i]);
            if (octet > 255) {
                return null;
            }
            out[i] = (byte) octet;
        }
        return out;
    }

    static Optional<InetAddress> v6(String text) {
        int compress = text.indexOf("::");
        if (compress >= 0 && text.indexOf("::", compress + 1) >= 0) {
            return Optional.empty();
        }
        List<Integer> groups = new ArrayList<>();
        if (compress < 0) {
            if (!collect(text, true, groups) || groups.size() != 8) {
                return Optional.empty();
            }
        } else {
            List<Integer> left = new ArrayList<>();
            List<Integer> right = new ArrayList<>();
            if (!collect(text.substring(0, compress), false, left)
                || !collect(text.substring(compress + 2), true, right) || left.size() + right.size() > 7) {
                return Optional.empty();
            }
            groups.addAll(left);
            for (int i = left.size() + right.size(); i < 8; i++) {
                groups.add(0);
            }
            groups.addAll(right);
        }
        byte[] bytes = new byte[16];
        for (int i = 0; i < 8; i++) {
            bytes[2 * i] = (byte) (groups.get(i) >> 8);
            bytes[2 * i + 1] = (byte) (int) groups.get(i);
        }
        return address(bytes);
    }

    private static boolean collect(String text, boolean allowIpv4Tail, List<Integer> out) {
        if (text.isEmpty()) {
            return true;
        }
        String[] parts = text.split(":", -1);
        for (int i = 0; i < parts.length; i++) {
            String part = parts[i];
            if (part.indexOf('.') >= 0) {
                byte[] tail = allowIpv4Tail && i == parts.length - 1 ? ipv4Bytes(part) : null;
                if (tail == null) {
                    return false;
                }
                out.add(((tail[0] & 0xff) << 8) | (tail[1] & 0xff));
                out.add(((tail[2] & 0xff) << 8) | (tail[3] & 0xff));
            } else if (HEX.matcher(part).matches()) {
                out.add(Integer.parseInt(part, 16));
            } else {
                return false;
            }
        }
        return true;
    }

    /**
     * Go's {@code net.IP.String()} form: dotted quad for IPv4 and IPv4-mapped IPv6, otherwise
     * lowercase hex with the first longest run of two or more zero groups compressed.
     */
    static String format(InetAddress address) {
        byte[] bytes = address.getAddress();
        if (bytes.length == 4) {
            return (bytes[0] & 0xff) + "." + (bytes[1] & 0xff) + "." + (bytes[2] & 0xff) + "."
                + (bytes[3] & 0xff);
        }
        int[] groups = new int[8];
        for (int i = 0; i < 8; i++) {
            groups[i] = ((bytes[2 * i] & 0xff) << 8) | (bytes[2 * i + 1] & 0xff);
        }
        int bestStart = -1;
        int bestLength = 0;
        for (int i = 0; i < 8;) {
            if (groups[i] != 0) {
                i++;
                continue;
            }
            int j = i;
            while (j < 8 && groups[j] == 0) {
                j++;
            }
            if (j - i > bestLength) {
                bestStart = i;
                bestLength = j - i;
            }
            i = j;
        }
        if (bestLength < 2) {
            bestStart = -1;
        }
        StringBuilder out = new StringBuilder();
        for (int i = 0; i < 8; i++) {
            if (i == bestStart) {
                out.append("::");
                i += bestLength - 1;
                continue;
            }
            if (out.length() > 0 && out.charAt(out.length() - 1) != ':') {
                out.append(':');
            }
            out.append(Integer.toHexString(groups[i]));
        }
        return out.toString();
    }

    private static Optional<InetAddress> address(byte[] bytes) {
        try {
            return Optional.of(InetAddress.getByAddress(bytes));
        } catch (UnknownHostException e) {
            return Optional.empty();
        }
    }
}
