package org.cao.backend.control;

/** Minimal JSON string encoding identical to ECMAScript {@code JSON.stringify} for strings. */
final class JsonText {
    private static final char[] HEX = "0123456789abcdef".toCharArray();

    private JsonText() { }

    static String quote(String value) {
        if (value == null) {
            return "null";
        }
        StringBuilder out = new StringBuilder(value.length() + 2).append('"');
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
                        out.append("\\u").append(HEX[c >> 12 & 15]).append(HEX[c >> 8 & 15])
                                .append(HEX[c >> 4 & 15]).append(HEX[c & 15]);
                    } else {
                        out.append(c);
                    }
                }
            }
        }
        return out.append('"').toString();
    }
}
