package org.cao.backend.dashboard;

/**
 * One validation error (DLS-VAL-001): an Appendix B code, a message that never echoes document values
 * (DLS-VAL-004), and the nearest YAML path.
 */
public record DashboardError(String code, String message, String path) { }
