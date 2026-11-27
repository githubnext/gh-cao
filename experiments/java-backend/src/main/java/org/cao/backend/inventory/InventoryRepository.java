package org.cao.backend.inventory;

import java.util.regex.Pattern;

/**
 * One repository as projected by the inventory boundary (id, full_name, archived, disabled, private,
 * pushed_at, default_branch). {@link #canonicalJson()} reproduces the bytes {@code JSON.stringify}
 * gives the comparison implementation, because the inventory version digest covers those bytes.
 */
public record InventoryRepository(
        long id,
        String fullName,
        boolean archived,
        boolean disabled,
        boolean isPrivate,
        String pushedAt,
        String defaultBranch) {
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final long MAX_SAFE_INTEGER = 9_007_199_254_740_991L;

    public InventoryRepository {
        if (id < 1 || id > MAX_SAFE_INTEGER) {
            throw new InventoryException("repository id must be a positive safe integer");
        }
        if (fullName == null || !REPOSITORY.matcher(fullName).matches()) {
            throw new InventoryException("repository full_name must use owner/repository form");
        }
    }

    public String canonicalJson() {
        StringBuilder out = new StringBuilder(160);
        out.append("{\"id\":").append(id).append(",\"full_name\":");
        string(out, fullName);
        out.append(",\"archived\":").append(archived)
                .append(",\"disabled\":").append(disabled)
                .append(",\"private\":").append(isPrivate)
                .append(",\"pushed_at\":");
        nullableString(out, pushedAt);
        out.append(",\"default_branch\":");
        nullableString(out, defaultBranch);
        return out.append('}').toString();
    }

    private static void nullableString(StringBuilder out, String value) {
        if (value == null) {
            out.append("null");
        } else {
            string(out, value);
        }
    }

    /** JSON.stringify escaping: no HTML escapes, lowercase hex, lone surrogates escaped. */
    private static void string(StringBuilder out, String value) {
        out.append('"');
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '"' -> out.append("\\\"");
                case '\\' -> out.append("\\\\");
                case '\b' -> out.append("\\b");
                case '\f' -> out.append("\\f");
                case '\n' -> out.append("\\n");
                case '\r' -> out.append("\\r");
                case '\t' -> out.append("\\t");
                default -> {
                    boolean lone = Character.isHighSurrogate(c)
                            ? !(i + 1 < value.length() && Character.isLowSurrogate(value.charAt(i + 1)))
                            : Character.isLowSurrogate(c) && !(i > 0 && Character.isHighSurrogate(value.charAt(i - 1)));
                    if (c < 0x20 || lone) {
                        out.append(String.format("\\u%04x", (int) c));
                    } else {
                        out.append(c);
                    }
                }
            }
        }
        out.append('"');
    }
}
