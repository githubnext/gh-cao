package org.cao.backend.policy;

/**
 * Dispatcher-envelope fields that standalone workflows and direct operations must never carry
 * (specs/control-architecture.md CAO-EXE-005/006). Blank values count as absent.
 */
public record DispatcherInputs(String correlationId, String centralRepository, String controlPlaneRunUrl) {

    public static DispatcherInputs none() {
        return new DispatcherInputs(null, null, null);
    }

    void requireAbsent(String role) {
        if (present(correlationId) || present(centralRepository) || present(controlPlaneRunUrl)) {
            throw new PolicyException(role + " must not carry a dispatcher envelope");
        }
    }

    private static boolean present(String value) {
        return value != null && !value.isEmpty();
    }
}
