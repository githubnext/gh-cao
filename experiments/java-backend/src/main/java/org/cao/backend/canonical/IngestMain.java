package org.cao.backend.canonical;

import java.nio.file.Path;
import java.util.Map;
import org.cao.backend.operational.PostgresEndpoint;

/**
 * Operator command that ingests one verified snapshot directory: {@code IngestMain <directory>}. It
 * reads {@code CAO_POSTGRES_URL} and {@code CAO_DATABASE_NAMESPACE} (default {@code hosted-dashboard})
 * and exits non-zero on any rejection. Use a login granted write access to the canonical tables.
 */
public final class IngestMain {
    private IngestMain() { }

    public static void main(String[] args) {
        if (args.length != 1) {
            System.err.println("usage: IngestMain <snapshot-directory>");
            System.exit(2);
        }
        Map<String, String> env = System.getenv();
        PostgresCanonicalStore store = PostgresCanonicalStore.open(
            PostgresEndpoint.parse(env.get("CAO_POSTGRES_URL")),
            env.getOrDefault("CAO_DATABASE_NAMESPACE", "hosted-dashboard"));
        IngestResult result = store.ingest(Path.of(args[0]), IngestOptions.defaults());
        System.out.println((result.replaced() ? "published revision " : "already current at revision ")
            + result.state().revision() + " " + result.counts());
    }
}
