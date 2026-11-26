package org.cao.backend.dashboard;

import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import org.snakeyaml.engine.v2.api.LoadSettings;
import org.snakeyaml.engine.v2.api.lowlevel.Parse;
import org.snakeyaml.engine.v2.common.ScalarStyle;
import org.snakeyaml.engine.v2.events.AliasEvent;
import org.snakeyaml.engine.v2.events.CollectionStartEvent;
import org.snakeyaml.engine.v2.events.DocumentStartEvent;
import org.snakeyaml.engine.v2.events.Event;
import org.snakeyaml.engine.v2.events.MappingStartEvent;
import org.snakeyaml.engine.v2.events.NodeEvent;
import org.snakeyaml.engine.v2.events.ScalarEvent;
import org.snakeyaml.engine.v2.events.SequenceStartEvent;
import org.snakeyaml.engine.v2.exceptions.YamlEngineException;

/**
 * Safe-loading YAML 1.2 reader for Dashboard Language documents (DLS-SAFE-001, DLS-DOC-001).
 *
 * <p>It consumes parser events only, so no object is ever constructed from a tag. Aliases, anchors and
 * explicit tags other than {@code !!str} are rejected, which also excludes cyclic structures and merge
 * expansion. Documents are bounded in size and nesting depth.
 */
public final class SafeYaml {
    public static final int MAX_BYTES = 2 * 1024 * 1024;
    public static final int MAX_DEPTH = 64;
    private static final String STR_TAG = "tag:yaml.org,2002:str";
    private static final Pattern INT = Pattern.compile("[-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+");
    private static final Pattern FLOAT = Pattern.compile(
            "[-+]?(\\.[0-9]+|[0-9]+(\\.[0-9]*)?)([eE][-+]?[0-9]+)?|[-+]?\\.(inf|Inf|INF)|\\.(nan|NaN|NAN)");
    private static final Pattern NULL = Pattern.compile("~|null|Null|NULL");
    private static final Pattern BOOL = Pattern.compile("true|True|TRUE|false|False|FALSE");

    /** Parse failure carrying an Appendix B code and the nearest path. */
    public static final class YamlFailure extends Exception {
        private static final long serialVersionUID = 1L;
        private final String code;
        private final String path;

        YamlFailure(String code, String message, String path) {
            super(message);
            this.code = code;
            this.path = path;
        }

        public String code() {
            return code;
        }

        public String path() {
            return path;
        }
    }

    private final Iterator<Event> events;
    private final List<DashboardError> duplicateKeys = new ArrayList<>();

    private SafeYaml(Iterator<Event> events) {
        this.events = events;
    }

    /** Result of {@link #parse}: the single document root plus duplicate-key errors (first occurrence wins). */
    public record Parsed(YamlNode root, List<DashboardError> duplicateKeys) { }

    public static Parsed parse(byte[] source) throws YamlFailure {
        if (source.length > MAX_BYTES) {
            throw new YamlFailure("DLS-E001", "document exceeds the size bound", "$");
        }
        String text;
        try {
            text = StandardCharsets.UTF_8.newDecoder()
                    .onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .decode(ByteBuffer.wrap(source)).toString();
        } catch (CharacterCodingException e) {
            throw new YamlFailure("DLS-E001", "document is not valid UTF-8", "$");
        }
        if (text.startsWith("\uFEFF")) {
            text = text.substring(1);
        }
        LoadSettings settings = LoadSettings.builder()
                .setAllowDuplicateKeys(true)
                .setAllowRecursiveKeys(false)
                .setMaxAliasesForCollections(0)
                .setCodePointLimit(MAX_BYTES)
                .build();
        try {
            Iterator<Event> it = new Parse(settings).parseString(text).iterator();
            SafeYaml reader = new SafeYaml(it);
            YamlNode root = reader.readSingleDocument();
            return new Parsed(root, reader.duplicateKeys);
        } catch (YamlFailure e) {
            throw e;
        } catch (YamlEngineException | IllegalStateException e) {
            throw new YamlFailure("DLS-E001", "document is not valid YAML 1.2", "$");
        }
    }

    private YamlNode readSingleDocument() throws YamlFailure {
        expect(events.next(), Event.ID.StreamStart);
        Event first = events.next();
        if (first.getEventId() == Event.ID.StreamEnd) {
            throw new YamlFailure("DLS-E002", "document must contain exactly one YAML document", "$");
        }
        if (!(first instanceof DocumentStartEvent)) {
            throw new YamlFailure("DLS-E001", "document is not valid YAML 1.2", "$");
        }
        YamlNode root = readNode(events.next(), "$", 0);
        expect(events.next(), Event.ID.DocumentEnd);
        Event after = events.next();
        if (after.getEventId() != Event.ID.StreamEnd) {
            throw new YamlFailure("DLS-E002", "document must contain exactly one YAML document", "$");
        }
        return root;
    }

    private static void expect(Event event, Event.ID id) throws YamlFailure {
        if (event.getEventId() != id) {
            throw new YamlFailure("DLS-E001", "document is not valid YAML 1.2", "$");
        }
    }

    private YamlNode readNode(Event event, String path, int depth) throws YamlFailure {
        if (depth > MAX_DEPTH) {
            throw new YamlFailure("DLS-E001", "document exceeds the nesting bound", path);
        }
        if (event instanceof AliasEvent) {
            throw new YamlFailure("DLS-E008", "YAML aliases are forbidden", path);
        }
        if (event instanceof NodeEvent node && node.getAnchor().isPresent()) {
            throw new YamlFailure("DLS-E008", "YAML anchors are forbidden", path);
        }
        if (event instanceof ScalarEvent scalar) {
            return readScalar(scalar, path);
        }
        if (event instanceof CollectionStartEvent collection) {
            if (collection.getTag().isPresent()) {
                throw new YamlFailure("DLS-E008", "explicit YAML tags are forbidden", path);
            }
            if (collection instanceof SequenceStartEvent) {
                return readSequence(path, depth);
            }
            if (collection instanceof MappingStartEvent) {
                return readMapping(path, depth);
            }
        }
        throw new YamlFailure("DLS-E001", "document is not valid YAML 1.2", path);
    }

    private YamlNode readSequence(String path, int depth) throws YamlFailure {
        List<YamlNode> items = new ArrayList<>();
        while (true) {
            Event next = events.next();
            if (next.getEventId() == Event.ID.SequenceEnd) {
                return new YamlNode.YamlSeq(items);
            }
            items.add(readNode(next, path + "[" + items.size() + "]", depth + 1));
        }
    }

    private YamlNode readMapping(String path, int depth) throws YamlFailure {
        Map<String, YamlNode> entries = new LinkedHashMap<>();
        while (true) {
            Event keyEvent = events.next();
            if (keyEvent.getEventId() == Event.ID.MappingEnd) {
                return new YamlNode.YamlMap(entries);
            }
            YamlNode key = readNode(keyEvent, path, depth + 1);
            if (!(key instanceof YamlNode.YamlScalar keyScalar) || !keyScalar.isString()) {
                throw new YamlFailure("DLS-E004", "mapping keys must be strings", path);
            }
            String keyPath = path + "." + Check.segment(keyScalar.text());
            YamlNode value = readNode(events.next(), keyPath, depth + 1);
            if (entries.containsKey(keyScalar.text())) {
                duplicateKeys.add(new DashboardError("DLS-E004", "duplicate mapping key", keyPath));
            } else {
                entries.put(keyScalar.text(), value);
            }
        }
    }

    private static YamlNode readScalar(ScalarEvent scalar, String path) throws YamlFailure {
        boolean quoted = scalar.getScalarStyle() != ScalarStyle.PLAIN;
        if (scalar.getTag().isPresent()) {
            if (!STR_TAG.equals(scalar.getTag().get())) {
                throw new YamlFailure("DLS-E008", "explicit YAML tags are forbidden", path);
            }
            return new YamlNode.YamlScalar(scalar.getValue(), YamlNode.Kind.STRING, true);
        }
        String value = scalar.getValue();
        if (quoted) {
            return new YamlNode.YamlScalar(value, YamlNode.Kind.STRING, true);
        }
        YamlNode.Kind kind = YamlNode.Kind.STRING;
        if (value.isEmpty() || NULL.matcher(value).matches()) {
            kind = YamlNode.Kind.NULL;
        } else if (BOOL.matcher(value).matches()) {
            kind = YamlNode.Kind.BOOL;
        } else if (INT.matcher(value).matches()) {
            kind = YamlNode.Kind.INT;
        } else if (FLOAT.matcher(value).matches()) {
            kind = YamlNode.Kind.FLOAT;
        }
        return new YamlNode.YamlScalar(value, kind, false);
    }
}
