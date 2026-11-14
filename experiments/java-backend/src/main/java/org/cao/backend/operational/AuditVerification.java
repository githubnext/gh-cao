package org.cao.backend.operational;

/** Summary of one integrity check over retained events; {@code lastHash} is the lowercase hex chain head. */
public record AuditVerification(long events, long firstSequence, long lastSequence,
                                String lastHash, int anchorsChecked, int anchorsExpired) { }
