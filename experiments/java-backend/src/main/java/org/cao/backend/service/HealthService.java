package org.cao.backend.service;

import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

/**
 * Evaluates liveness/storage health and readiness. Any probe failure or timeout is unhealthy and
 * never reported as available data (fail closed).
 */
public final class HealthService implements AutoCloseable {
    private final OperationalProbe operationalProbe;
    private final DataProbe dataProbe;
    private final long timeoutMillis;
    private final ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor();

    public HealthService(OperationalProbe operationalProbe, DataProbe dataProbe, long timeoutMillis) {
        this.operationalProbe = operationalProbe;
        this.dataProbe = dataProbe;
        this.timeoutMillis = timeoutMillis;
    }

    public HealthReport evaluate() {
        OperationalStatus operational = call(operationalProbe::status);
        DataStatus data = call(dataProbe::state);
        boolean connected = operational != null && operational.connected();
        // Health (Go parity): unhealthy when the operational dependency or data state cannot be evaluated.
        boolean healthy = connected && data != null;
        // Readiness additionally requires an active dataset and completed recovery.
        boolean ready = healthy && data.available() && operational.recoveryReady();
        return new HealthReport(
                healthy,
                ready,
                operational == null ? OperationalStatus.unconfigured() : operational,
                data == null ? DataStatus.empty() : data);
    }

    private <T> T call(ThrowingSupplier<T> supplier) {
        Future<T> future = executor.submit(supplier::get);
        try {
            return future.get(timeoutMillis, TimeUnit.MILLISECONDS);
        } catch (TimeoutException e) {
            future.cancel(true);
            return null;
        } catch (InterruptedException e) {
            future.cancel(true);
            Thread.currentThread().interrupt();
            return null;
        } catch (ExecutionException e) {
            return null;
        }
    }

    @Override
    public void close() {
        executor.shutdownNow();
    }

    @FunctionalInterface
    private interface ThrowingSupplier<T> {
        T get() throws Exception;
    }
}
