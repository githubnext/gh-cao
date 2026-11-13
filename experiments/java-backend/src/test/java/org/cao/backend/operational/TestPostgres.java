package org.cao.backend.operational;

import java.io.IOException;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Comparator;
import java.util.List;
import java.util.concurrent.TimeUnit;
import java.util.stream.Stream;

/**
 * Starts a disposable PostgreSQL instance on numeric loopback in a temporary directory. It uses
 * local PostgreSQL binaries (CAO_TEST_POSTGRES_BIN or /usr/lib/postgresql/&lt;n&gt;/bin) and fails,
 * never skips, when they are unavailable: a missing backend is not passing evidence.
 */
public final class TestPostgres implements AutoCloseable {
    public static final String OWNER = "cao_owner";
    public static final String RUNTIME = "cao_runtime";

    private final Path data;
    private final Path bin;
    private final int port;

    private TestPostgres(Path data, Path bin, int port) {
        this.data = data;
        this.bin = bin;
        this.port = port;
    }

    public static TestPostgres start() throws Exception {
        Path bin = locateBin();
        Path data = Files.createTempDirectory("cao-java-pg-");
        int port;
        try (ServerSocket socket = new ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))) {
            port = socket.getLocalPort();
        }
        TestPostgres fixture = new TestPostgres(data, bin, port);
        try {
            run(List.of(bin.resolve("initdb").toString(), "-D", data.resolve("db").toString(),
                "-U", OWNER, "--auth=trust", "-E", "UTF8", "--no-sync"));
            run(List.of(bin.resolve("pg_ctl").toString(), "-D", data.resolve("db").toString(),
                "-o", "-p " + port + " -c listen_addresses=127.0.0.1 -c unix_socket_directories="
                    + data + " -c fsync=off", "-l", data.resolve("pg.log").toString(), "-w",
                "start"));
        } catch (IOException | RuntimeException e) {
            fixture.close();
            throw e;
        }
        return fixture;
    }

    private static Path locateBin() throws IOException {
        String configured = System.getenv("CAO_TEST_POSTGRES_BIN");
        if (configured != null && !configured.isBlank()) {
            return Path.of(configured);
        }
        Path root = Path.of("/usr/lib/postgresql");
        if (Files.isDirectory(root)) {
            try (Stream<Path> versions = Files.list(root)) {
                return versions.filter(p -> Files.isExecutable(p.resolve("bin/initdb")))
                    .filter(p -> p.getFileName().toString().matches("[0-9]+"))
                    .max(Comparator.comparingInt(p -> Integer.parseInt(p.getFileName().toString())))
                    .map(p -> p.resolve("bin"))
                    .orElseThrow(() -> new IllegalStateException(unavailable()));
            }
        }
        throw new IllegalStateException(unavailable());
    }

    private static String unavailable() {
        return "PostgreSQL server binaries are required for the operational store tests; set "
            + "CAO_TEST_POSTGRES_BIN to a directory with initdb and pg_ctl";
    }

    private static void run(List<String> command) throws IOException {
        Process process = new ProcessBuilder(command).redirectErrorStream(true).start();
        byte[] output = process.getInputStream().readAllBytes();
        try {
            if (!process.waitFor(120, TimeUnit.SECONDS) || process.exitValue() != 0) {
                throw new IllegalStateException(
                    "postgres fixture command failed: " + new String(output));
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException(e);
        }
    }

    public int port() {
        return port;
    }

    /** Connection URL for a role; trust authentication is limited to this loopback instance. */
    public String url(String user, String database) {
        return "postgres://" + user + "@127.0.0.1:" + port + "/" + database;
    }

    public String endpointUrl() {
        return url(OWNER, "postgres");
    }

    public PostgresEndpoint endpoint() {
        return PostgresEndpoint.parse(endpointUrl());
    }

    /** Stops the server immediately, simulating a lost database. */
    public void stop() throws IOException {
        stopServer();
    }

    void stopServer() throws IOException {
        run(List.of(bin.resolve("pg_ctl").toString(), "-D", data.resolve("db").toString(), "-m",
            "immediate", "-w", "stop"));
    }

    @Override
    public void close() throws IOException {
        try {
            if (Files.exists(data.resolve("db/postmaster.pid"))) {
                stopServer();
            }
        } finally {
            if (Files.exists(data)) {
                try (Stream<Path> paths = Files.walk(data)) {
                    paths.sorted(Comparator.reverseOrder()).forEach(p -> p.toFile().delete());
                }
            }
        }
    }
}
