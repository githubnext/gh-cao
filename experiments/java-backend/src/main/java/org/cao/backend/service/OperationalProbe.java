package org.cao.backend.service;

/** Operational-state contract used by health; adapters own all protocol details. */
public interface OperationalProbe {
    /** Returns current status or throws if the dependency cannot be evaluated. */
    OperationalStatus status() throws Exception;
}
