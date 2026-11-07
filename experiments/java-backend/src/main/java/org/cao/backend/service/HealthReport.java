package org.cao.backend.service;

/** Result of one health or readiness evaluation. */
public record HealthReport(boolean healthy, boolean ready, OperationalStatus operational, DataStatus data) {
}
