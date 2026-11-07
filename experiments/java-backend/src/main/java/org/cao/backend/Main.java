package org.cao.backend;

import java.util.concurrent.CountDownLatch;
import org.cao.backend.http.HealthServer;
import org.cao.backend.http.ServerConfig;
import org.cao.backend.service.DataStatus;
import org.cao.backend.service.HealthService;
import org.cao.backend.service.OperationalStatus;

/**
 * Entry point. No storage adapter exists yet, so the service reports unconfigured operational state
 * and no data (unhealthy / not ready) rather than pretending to be backed by a database.
 */
public final class Main {
    private Main() {
    }

    public static void main(String[] args) throws Exception {
        ServerConfig config = ServerConfig.fromEnv(System.getenv());
        HealthService health = new HealthService(
                OperationalStatus::unconfigured, DataStatus::empty, config.probeTimeoutMillis());
        HealthServer server = new HealthServer(config, health);
        CountDownLatch stopped = new CountDownLatch(1);
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            server.close();
            stopped.countDown();
        }));
        server.start();
        System.out.println("cao-java-backend listening on " + config.host() + ":" + server.port());
        stopped.await();
    }
}
