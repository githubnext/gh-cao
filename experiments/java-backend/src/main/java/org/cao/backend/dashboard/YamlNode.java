package org.cao.backend.dashboard;

import java.util.List;
import java.util.Map;
import java.util.OptionalDouble;
import java.util.OptionalLong;

/** Inert YAML tree produced by {@link SafeYaml}; it never carries executable or aliased content. */
public sealed interface YamlNode permits YamlNode.YamlMap, YamlNode.YamlSeq, YamlNode.YamlScalar {
    /** YAML 1.2 core-schema scalar classes; quoted or explicitly string-tagged scalars are always STRING. */
    enum Kind { STRING, BOOL, INT, FLOAT, NULL }

    /** Insertion-ordered mapping with unique string keys. */
    record YamlMap(Map<String, YamlNode> entries) implements YamlNode {
        public YamlNode get(String key) {
            return entries.get(key);
        }
    }

    /** Sequence of nodes. */
    record YamlSeq(List<YamlNode> items) implements YamlNode { }

    /** Scalar with its resolved kind; {@code quoted} records the source style (DLS-DOC-003, DLS-DOC-010). */
    record YamlScalar(String text, Kind kind, boolean quoted) implements YamlNode {
        public boolean isString() {
            return kind == Kind.STRING;
        }

        public OptionalLong asLong() {
            if (kind != Kind.INT) {
                return OptionalLong.empty();
            }
            try {
                String t = text.startsWith("+") ? text.substring(1) : text;
                if (t.startsWith("0x") || t.startsWith("0X")) {
                    return OptionalLong.of(Long.parseLong(t.substring(2), 16));
                }
                if (t.startsWith("0o")) {
                    return OptionalLong.of(Long.parseLong(t.substring(2), 8));
                }
                return OptionalLong.of(Long.parseLong(t));
            } catch (NumberFormatException e) {
                return OptionalLong.empty();
            }
        }

        /** Finite numeric value of an INT or FLOAT scalar; infinities and NaN are absent. */
        public OptionalDouble asFiniteDouble() {
            if (kind == Kind.INT) {
                OptionalLong l = asLong();
                return l.isPresent() ? OptionalDouble.of((double) l.getAsLong()) : OptionalDouble.empty();
            }
            if (kind != Kind.FLOAT) {
                return OptionalDouble.empty();
            }
            try {
                double d = Double.parseDouble(text);
                return Double.isFinite(d) ? OptionalDouble.of(d) : OptionalDouble.empty();
            } catch (NumberFormatException e) {
                return OptionalDouble.empty();
            }
        }
    }
}
