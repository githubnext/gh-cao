package org.cao.backend.policy;

/** Compact JSON for inventory rows with ECMAScript {@code JSON.stringify} string escaping. */
final class RepositoryJson {
    private RepositoryJson() { }

    static String compact(RepositoryRecord repository) {
        StringBuilder out = new StringBuilder();
        out.append("{\"id\":").append(repository.id());
        out.append(",\"full_name\":");
        string(out, repository.fullName());
        out.append(",\"archived\":").append(repository.archived());
        out.append(",\"disabled\":").append(repository.disabled());
        out.append(",\"private\":").append(repository.isPrivate());
        out.append(",\"pushed_at\":");
        nullable(out, repository.pushedAt());
        out.append(",\"default_branch\":");
        nullable(out, repository.defaultBranch());
        return out.append('}').toString();
    }

    private static void nullable(StringBuilder out, String value) {
        if (value == null) {
            out.append("null");
        } else {
            string(out, value);
        }
    }

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
                    boolean loneSurrogate = Character.isSurrogate(c)
                            && !(Character.isHighSurrogate(c) && i + 1 < value.length()
                                    && Character.isLowSurrogate(value.charAt(i + 1)))
                            && !(Character.isLowSurrogate(c) && i > 0 && Character.isHighSurrogate(value.charAt(i - 1)));
                    if (c < 0x20 || loneSurrogate) {
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
