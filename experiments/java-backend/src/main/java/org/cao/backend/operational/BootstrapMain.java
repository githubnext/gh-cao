package org.cao.backend.operational;

import java.util.Map;
import org.cao.backend.canonical.CanonicalBootstrap;

/**
 * Owner-run provisioning entry point. Reads the owner connection from {@code CAO_POSTGRES_URL} and
 * the namespace from {@code CAO_OPERATIONAL_NAMESPACE} (default {@code CAO_DATABASE_NAMESPACE},
 * then {@code hosted-dashboard}). Never run with the runtime login.
 */
public final class BootstrapMain {
    private BootstrapMain() { }

    public static void main(String[] args) {
        Map<String, String> env = System.getenv();
        String url = env.get("CAO_POSTGRES_URL");
        String canonical = env.getOrDefault("CAO_DATABASE_NAMESPACE", "hosted-dashboard");
        String namespace = env.getOrDefault("CAO_OPERATIONAL_NAMESPACE", canonical);
        PostgresEndpoint endpoint = PostgresEndpoint.parse(url);
        OperationalBootstrap.provision(endpoint, namespace);
        CanonicalBootstrap.provision(endpoint, canonical);
        System.out.println("operational and canonical namespaces provisioned");
    }
}
