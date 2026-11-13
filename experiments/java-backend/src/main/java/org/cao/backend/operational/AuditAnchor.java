package org.cao.backend.operational;

/** A previously reported chain position kept outside the database. */
public record AuditAnchor(long sequence, String hash) { }
