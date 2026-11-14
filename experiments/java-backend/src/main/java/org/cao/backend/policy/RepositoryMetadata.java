package org.cao.backend.policy;

import java.nio.charset.StandardCharsets;
import java.util.regex.Pattern;

/**
 * Repository inventory evidence. {@link #canonicalJson()} reproduces the member order and string escaping of
 * the JavaScript {@code JSON.stringify} used to digest the inventory in the comparison implementation, so
 * that inventory versions are comparable across implementations.
 */
public record RepositoryMetadata(
        long id,
        String fullName,
        boolean archived,
        boolean disabled,
        boolean isPrivate,
        String pushedAt,
        String defaultBranch) {
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final long MAX_SAFE_INTEGER = 9_007_199_254_740_991L;

    public RepositoryMetadata {
        if (id <= 0 || id > MAX_SAFE_INTEGER) {
            throw new PolicyException("repository id must be a positive safe integer");
        }
        if (fullName == null || !REPOSITORY.matcher(fullName).matches()) {
            throw new PolicyException("repository full name must use owner/repository form");
        }
    }

    public String canonicalJson() {
        StringBuilder out = new StringBuilder(160);
        out.append("{\"id\":").append(id).append(",\"full_name\":");
        quote(out, fullName);
        out.append(",\"archived\":").append(archived).append(",\"disabled\":").append(disabled);
        out.append(",\"private\":").append(isPrivate).append(",\"pushed_at\":");
        quote(out, pushedAt);
        out.append(",\"default_branch\":");
        quote(out, defaultBranch);
        return out.append('}').toString();
    }

    byte[] canonicalBytes() {
        return canonicalJson().getBytes(StandardCharsets.UTF_8);
    }

    private static void quote(StringBuilder out, String value) {
        if (value == null) {
            out.append("null");
            return;
        }
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
                            ? i + 1 >= value.length() || !Character.isLowSurrogate(value.charAt(i + 1))
                            : Character.isLowSurrogate(c) && (i == 0 || !Character.isHighSurrogate(value.charAt(i - 1)));
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
