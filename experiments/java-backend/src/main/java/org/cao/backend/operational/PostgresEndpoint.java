package org.cao.backend.operational;

import java.net.InetAddress;
import java.net.URI;
import java.net.URISyntaxException;
import java.net.URLDecoder;
import java.net.UnknownHostException;
import java.nio.charset.StandardCharsets;
import java.util.Properties;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * A validated PostgreSQL connection target parsed from a libpq-style {@code postgres://} URL.
 * Remote hosts require {@code sslmode=verify-full}; only loopback and {@code localhost} may use
 * weaker modes (specs/server-operational-storage.md). Unix sockets, multiple hosts and unknown
 * parameters are rejected rather than ignored. The password is never part of {@link #toString()}.
 */
public final class PostgresEndpoint {
    private static final Set<String> SSL_MODES =
        Set.of("disable", "allow", "prefer", "require", "verify-ca", "verify-full");
    private static final Pattern SEARCH_PATH = Pattern.compile("-c search_path=[A-Za-z_][A-Za-z0-9_]*");
    private static final Pattern IPV4 = Pattern.compile("[0-9]{1,3}(\\.[0-9]{1,3}){3}");

    private final String jdbcUrl;
    private final Properties properties;
    private final String redacted;

    private PostgresEndpoint(String jdbcUrl, Properties properties, String redacted) {
        this.jdbcUrl = jdbcUrl;
        this.properties = properties;
        this.redacted = redacted;
    }

    public static PostgresEndpoint parse(String url) {
        if (url == null || url.isBlank()) {
            throw new OperationalStoreException("postgres connection configuration is required");
        }
        URI uri;
        try {
            uri = new URI(url.strip());
        } catch (URISyntaxException e) {
            throw new OperationalStoreException("invalid postgres connection configuration");
        }
        String scheme = uri.getScheme();
        if (!"postgres".equals(scheme) && !"postgresql".equals(scheme)) {
            throw new OperationalStoreException("postgres URL must use the postgres:// scheme");
        }
        String host = uri.getHost();
        if (host == null || host.isEmpty() || host.contains(",")) {
            throw new OperationalStoreException(
                "postgres URL requires exactly one TCP host (unix sockets are unsupported)");
        }
        boolean loopback = loopback(host);
        String database = uri.getPath() == null ? "" : uri.getPath().replaceFirst("^/", "");
        if (database.isEmpty() || database.contains("/")) {
            throw new OperationalStoreException("postgres URL requires a database name");
        }
        Properties props = new Properties();
        credentials(uri, props);
        String sslMode = null;
        String query = uri.getRawQuery();
        if (query != null && !query.isEmpty()) {
            for (String pair : query.split("&", -1)) {
                int eq = pair.indexOf('=');
                String name = decode(eq < 0 ? pair : pair.substring(0, eq));
                String value = eq < 0 ? "" : decode(pair.substring(eq + 1));
                switch (name) {
                    case "sslmode" -> {
                        if (!SSL_MODES.contains(value)) {
                            throw new OperationalStoreException("unsupported sslmode");
                        }
                        sslMode = value;
                        props.setProperty("sslmode", value);
                    }
                    case "sslrootcert" -> props.setProperty("sslrootcert", value);
                    case "options" -> {
                        if (!SEARCH_PATH.matcher(value).matches()) {
                            throw new OperationalStoreException(
                                "only options=-c search_path=<schema> is supported");
                        }
                        props.setProperty("options", value);
                    }
                    default -> throw new OperationalStoreException(
                        "unsupported postgres URL parameter: " + name);
                }
            }
        }
        if (!loopback && !"verify-full".equals(sslMode)) {
            throw new OperationalStoreException("postgres verified TLS with hostname verification "
                + "(sslmode=verify-full) is required for non-loopback connections");
        }
        props.setProperty("connectTimeout", "5");
        props.setProperty("socketTimeout", "30");
        props.setProperty("ApplicationName", "cao-java-backend");
        int port = uri.getPort() < 0 ? 5432 : uri.getPort();
        String jdbc = "jdbc:postgresql://" + host + ":" + port + "/" + database;
        return new PostgresEndpoint(jdbc, props, "postgres://" + host + ":" + port + "/" + database);
    }

    /** Numeric addresses are checked without DNS; any other name is remote except localhost. */
    static boolean loopback(String host) {
        if ("localhost".equalsIgnoreCase(host)) {
            return true;
        }
        boolean numeric = IPV4.matcher(host).matches() || host.startsWith("[");
        if (!numeric) {
            return false;
        }
        try {
            String bare = host.startsWith("[") ? host.substring(1, host.length() - 1) : host;
            return InetAddress.getByName(bare).isLoopbackAddress();
        } catch (UnknownHostException e) {
            return false;
        }
    }

    private static void credentials(URI uri, Properties props) {
        String info = uri.getRawUserInfo();
        if (info == null) {
            return;
        }
        int colon = info.indexOf(':');
        String user = decode(colon < 0 ? info : info.substring(0, colon));
        if (!user.isEmpty()) {
            props.setProperty("user", user);
        }
        if (colon >= 0) {
            props.setProperty("password", decode(info.substring(colon + 1)));
        }
    }

    private static String decode(String raw) {
        return URLDecoder.decode(raw.replace("+", "%2B"), StandardCharsets.UTF_8);
    }

    String jdbcUrl() {
        return jdbcUrl;
    }

    Properties connectionProperties() {
        Properties copy = new Properties();
        copy.putAll(properties);
        return copy;
    }

    @Override
    public String toString() {
        return redacted;
    }
}
