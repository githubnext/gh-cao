package org.cao.backend.dashboard;

import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.regex.Pattern;

/** Shared structural checking helpers; every error is path-addressed and never echoes document values. */
final class Check {
    static final String E003 = "DLS-E003";
    static final String E004 = "DLS-E004";
    static final String E005 = "DLS-E005";
    static final String E006 = "DLS-E006";
    static final String E007 = "DLS-E007";
    static final String E008 = "DLS-E008";
    static final String E009 = "DLS-E009";
    static final String E010 = "DLS-E010";
    static final String E011 = "DLS-E011";
    static final String E013 = "DLS-E013";
    static final String E015 = "DLS-E015";

    private static final Pattern SAFE_KEY = Pattern.compile("[A-Za-z0-9_./-]{1,64}");

    private final List<DashboardError> errors = new ArrayList<>();

    List<DashboardError> errors() {
        return errors;
    }

    void error(String code, String message, String path) {
        errors.add(new DashboardError(code, message, path));
    }

    /** Keys are echoed only when they are short and path-safe; document content is never reflected. */
    static String segment(String raw) {
        return SAFE_KEY.matcher(raw).matches() ? raw : "<key>";
    }

    static String child(String path, String key) {
        return path + "." + segment(key);
    }

    static String index(String path, int i) {
        return path + "[" + i + "]";
    }

    YamlNode.YamlMap map(YamlNode node, String path, String what) {
        if (node instanceof YamlNode.YamlMap m) {
            return m;
        }
        error(E003, what + " must be a mapping", path);
        return null;
    }

    YamlNode.YamlSeq seq(YamlNode node, String path, String what, boolean nonEmpty) {
        if (node instanceof YamlNode.YamlSeq s) {
            if (nonEmpty && s.items().isEmpty()) {
                error(E003, what + " must be a non-empty sequence", path);
                return null;
            }
            return s;
        }
        error(E003, what + " must be a sequence", path);
        return null;
    }

    /** DLS-DOC-007: unknown keys are rejected. */
    void keys(YamlNode.YamlMap m, Set<String> allowed, String path) {
        for (String key : m.entries().keySet()) {
            if (!allowed.contains(key)) {
                error(E004, "unknown key", child(path, key));
            }
        }
    }

    /** Returns the text of a present string scalar, or null after recording an error. */
    String string(YamlNode.YamlMap m, String key, String path, boolean required) {
        YamlNode node = m.get(key);
        String at = child(path, key);
        if (node == null) {
            if (required) {
                error(E003, "missing required field", at);
            }
            return null;
        }
        return stringValue(node, at, key);
    }

    String stringValue(YamlNode node, String at, String what) {
        if (node instanceof YamlNode.YamlScalar s && s.isString() && !s.text().isBlank()) {
            return s.text();
        }
        error(E003, what + " must be a non-empty string", at);
        return null;
    }

    String identifier(YamlNode.YamlMap m, String key, String path, boolean required) {
        String text = string(m, key, path, required);
        if (text != null && !DashboardVocabulary.IDENTIFIER.matcher(text).matches()) {
            error(E005, key + " must be a canonical kebab-case identifier", child(path, key));
            return null;
        }
        return text;
    }

    String oneOf(YamlNode.YamlMap m, String key, Set<String> allowed, String path, boolean required) {
        String text = string(m, key, path, required);
        if (text != null && !allowed.contains(text)) {
            error(E005, key + " must use a canonical enumerated value", child(path, key));
            return null;
        }
        return text;
    }

    Boolean bool(YamlNode.YamlMap m, String key, String path) {
        YamlNode node = m.get(key);
        if (node == null) {
            return null;
        }
        if (node instanceof YamlNode.YamlScalar s && s.kind() == YamlNode.Kind.BOOL) {
            return Boolean.valueOf(s.text().equalsIgnoreCase("true"));
        }
        error(E003, key + " must be a boolean", child(path, key));
        return null;
    }

    /** Returns a positive integer value or null after recording an error with {@code code}. */
    Long positiveInt(YamlNode node, String at, String what, String code) {
        if (node instanceof YamlNode.YamlScalar s && s.asLong().isPresent() && s.asLong().getAsLong() > 0) {
            return s.asLong().getAsLong();
        }
        error(code, what + " must be a positive integer", at);
        return null;
    }

    static boolean isScalarLiteral(YamlNode node) {
        return node instanceof YamlNode.YamlScalar s && s.kind() != YamlNode.Kind.NULL
                && (s.kind() != YamlNode.Kind.FLOAT || s.asFiniteDouble().isPresent());
    }

    /** DLS-QUERY-001 / DLS-VIEW-034: combined subject, objective and acceptance length bound. */
    void metadataBound(YamlNode.YamlMap m, String path) {
        int total = 0;
        for (String key : List.of("subject", "objective", "acceptance")) {
            if (m.get(key) instanceof YamlNode.YamlScalar s && s.isString()) {
                total += s.text().codePointCount(0, s.text().length());
            }
        }
        if (total > DashboardVocabulary.MAX_METADATA_CHARS) {
            error(E003, "subject, objective and acceptance exceed the 512 character bound", path);
        }
    }
}
