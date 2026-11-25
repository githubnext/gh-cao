package org.cao.backend.http;

import java.util.Map;
import org.cao.backend.operational.PostgresEndpoint;
import org.cao.backend.operational.PostgresOperationalStore;

/** Entry point: reads listener and storage settings from the environment and serves until terminated. */
public final class Main {
    static final String POSTGRES_URL = "CAO_POSTGRES_URL";
    static final String DATABASE_NAMESPACE = "CAO_DATABASE_NAMESPACE";
    static final String OPERATIONAL_NAMESPACE = "CAO_OPERATIONAL_NAMESPACE";
    static final String POLICY_PATH = "CAO_POLICY_PATH";
    static final String DEFAULT_NAMESPACE = "hosted-dashboard";

    private Main() { }

    public static void main(String[] args) throws Exception {
        Map<String, String> env = System.getenv();
        ServerConfig config = ServerConfig.fromEnvironment(env);
        PostgresOperationalStore store = openStore(env);
        ServiceState state = store == null ? ServiceState.unconfigured() : new PostgresServiceState(store);
        ApiServer server = new ApiServer(config, state, loadCors(env));
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            server.stop(config.shutdownGraceSeconds());
            if (store != null) {
                store.close();
            }
        }));
        server.start();
        System.out.println("listening on " + config.bind() + ":" + server.port());
    }

    /**
     * The cross-origin policy comes only from the reviewed policy document (specs/server-cors.md §3.1);
     * the environment supplies the document's location, never origins. An unreadable or invalid document
     * fails startup; without one the server stays same-origin.
     */
    static CorsPolicy loadCors(Map<String, String> env) {
        String path = env.get(POLICY_PATH);
        if (path == null || path.isBlank()) {
            return CorsPolicy.sameOrigin();
        }
        try {
            return HostPolicy.loadCors(java.nio.file.Files.readAllBytes(java.nio.file.Path.of(path)));
        } catch (java.io.IOException e) {
            throw new IllegalArgumentException(POLICY_PATH + " is not readable", e);
        }
    }

    /**
     * Opens pre-provisioned operational storage when {@code CAO_POSTGRES_URL} is set. A configured
     * but invalid, insecure or unprovisioned store fails startup rather than silently running without it.
     */
    static PostgresOperationalStore openStore(Map<String, String> env) {
        String url = env.get(POSTGRES_URL);
        if (url == null || url.isBlank()) {
            return null;
        }
        String canonical = env.getOrDefault(DATABASE_NAMESPACE, DEFAULT_NAMESPACE);
        String namespace = env.getOrDefault(OPERATIONAL_NAMESPACE, canonical);
        return PostgresOperationalStore.open(PostgresEndpoint.parse(url), namespace);
    }
}
