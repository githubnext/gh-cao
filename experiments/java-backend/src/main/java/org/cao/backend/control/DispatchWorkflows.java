package org.cao.backend.control;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Reads the compiled-workflow-declared worker list ({@code safe-outputs.dispatch-workflow.workflows})
 * from workflow frontmatter. The declaration is gh-aw-owned; CAO only intersects it with policy.
 */
public final class DispatchWorkflows {
    private static final Pattern DELIMITER = Pattern.compile("^---\\s*$");
    private static final Pattern INLINE = Pattern.compile("^    workflows:\\s*\\[(.*)]\\s*$");
    private static final Pattern ITEM = Pattern.compile("^\\s{4,6}-\\s+(.+)$");

    private DispatchWorkflows() { }

    public static List<String> parse(String source) {
        String[] lines = source.split("\n", -1);
        boolean frontmatter = false;
        boolean safeOutputs = false;
        boolean dispatch = false;
        boolean listed = false;
        LinkedHashSet<String> workers = new LinkedHashSet<>();
        for (int index = 0; index < lines.length; index++) {
            String line = lines[index];
            if (index == 0 && DELIMITER.matcher(line).matches()) {
                frontmatter = true;
                continue;
            }
            if (frontmatter && DELIMITER.matcher(line).matches()) {
                break;
            }
            if (!frontmatter) {
                continue;
            }
            if (line.startsWith("safe-outputs:")) {
                safeOutputs = true;
                continue;
            }
            if (safeOutputs && !line.isEmpty() && !Character.isWhitespace(line.charAt(0))) {
                safeOutputs = false;
            }
            if (safeOutputs && line.startsWith("  dispatch-workflow:")) {
                dispatch = true;
                continue;
            }
            if (dispatch && line.startsWith("  ") && line.length() > 2 && !Character.isWhitespace(line.charAt(2))) {
                dispatch = false;
                listed = false;
            }
            Matcher inline = INLINE.matcher(line);
            if (dispatch && inline.matches()) {
                for (String value : inline.group(1).split(",")) {
                    String name = unquote(value.trim());
                    if (!name.isEmpty()) {
                        workers.add(name);
                    }
                }
                continue;
            }
            if (dispatch && line.matches("^    workflows:\\s*$")) {
                listed = true;
                continue;
            }
            Matcher item = ITEM.matcher(line);
            if (listed && item.matches()) {
                workers.add(unquote(item.group(1).trim()));
                continue;
            }
            if (listed) {
                listed = false;
            }
        }
        return new ArrayList<>(workers);
    }

    private static String unquote(String value) {
        return value.replaceAll("^['\"]|['\"]$", "");
    }
}
