package org.cao.backend.service;

/** Canonical-storage contract used by health. */
public interface DataProbe {
    /** Returns the active dataset state or throws if storage cannot be evaluated. */
    DataStatus state() throws Exception;
}
