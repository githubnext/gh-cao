package org.cao.backend.policy;

/** Persistent output modes, ordered from least to most permissive. Debug is deliberately absent. */
public enum Mode {
    REVIEW("review"),
    LIVE("live");

    private final String wire;

    Mode(String wire) {
        this.wire = wire;
    }

    public String wire() {
        return wire;
    }

    public static Mode parse(String value, String name) {
        for (Mode mode : values()) {
            if (mode.wire.equals(value)) {
                return mode;
            }
        }
        throw new PolicyException(name + " must be review or live");
    }

    public Mode lesser(Mode other) {
        return ordinal() <= other.ordinal() ? this : other;
    }
}
