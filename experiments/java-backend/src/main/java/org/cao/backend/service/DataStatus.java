package org.cao.backend.service;

import java.time.Instant;

/** Canonical dataset state. {@code evaluatedAt} is only meaningful when the dataset is available. */
public record DataStatus(boolean available, Instant evaluatedAt) {
    public static DataStatus empty() {
        return new DataStatus(false, null);
    }
}
