package org.cao.backend.dashboard;

import java.util.List;

/**
 * Outcome of validating one dashboard document.
 *
 * <p>{@code ok} means no error was found by the rules this validator implements; it is not a conformance
 * claim. {@code limitations} lists document parts that were accepted without validation, so a caller can
 * tell a checked part from an unchecked one.
 */
public record ValidationResult(boolean ok, List<DashboardError> errors, List<String> limitations) {
    public ValidationResult {
        errors = List.copyOf(errors);
        limitations = List.copyOf(limitations);
    }

    public boolean hasCode(String code) {
        return errors.stream().anyMatch(e -> e.code().equals(code));
    }
}
