package org.cao.backend.policy;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.nio.ByteBuffer;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.HexFormat;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Strict parser for the control-plane subset of CAO policy (specs/control-architecture.md §5).
 * Anything outside the implemented subset is rejected, never ignored.
 */
public final class PolicyParser {
    private static final Pattern OWNER = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*$");
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final Pattern SLUG = Pattern.compile("^[a-z0-9]+(?:-[a-z0-9]+)*$");
    private static final Pattern GH_AW_VERSION = Pattern.compile("^v[0-9]+\\.[0-9]+\\.[0-9]+(?:-[0-9A-Za-z.-]+)?$");
    private static final String SCHEMA_ID =
            "https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json";
    private static final Set<String> UNSUPPORTED_CONTROL_KEYS = Set.of(
            "activity", "inventory", "web", "workflows", "operations", "publishing",
            "marketplace", "webhook-gateway", "token-broker");
    private static final Set<String> UNSUPPORTED_CAMPAIGN_KEYS = Set.of(
            "icon", "deploy", "workflow-values");

    private static final ObjectMapper MAPPER = new ObjectMapper()
            .enable(JsonParser.Feature.STRICT_DUPLICATE_DETECTION)
            .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS);

    private PolicyParser() { }

    public static ControlPolicy parse(byte[] json) {
        String text = decodeUtf8(json);
        JsonNode root;
        try {
            root = MAPPER.readTree(text);
        } catch (JsonProcessingException e) {
            throw new PolicyException("policy is not valid JSON without duplicate keys");
        }
        if (root == null || !root.isObject()) {
            throw new PolicyException("policy root must be an object");
        }
        rejectExpressions(root);
        keys(root, "policy", Set.of("$schema", "version", "gh-aw-version", "control-plane", "target-authority"));
        if (root.has("$schema") && !(root.get("$schema").isTextual() && SCHEMA_ID.equals(root.get("$schema").asText()))) {
            throw new PolicyException("policy.$schema is not the CAO schema identifier");
        }
        JsonNode version = root.get("version");
        if (version == null || !version.isIntegralNumber() || !version.canConvertToInt() || version.asInt() != 1) {
            throw new PolicyException("policy.version must be integer 1");
        }
        if (root.has("gh-aw-version")
                && !(root.get("gh-aw-version").isTextual()
                        && GH_AW_VERSION.matcher(root.get("gh-aw-version").asText()).matches())) {
            throw new PolicyException("policy.gh-aw-version must be a v-prefixed semantic version");
        }
        if (root.has("target-authority")) {
            throw new PolicyException("target-authority is not supported by the Java backend yet");
        }
        if (!root.has("control-plane")) {
            throw new PolicyException("policy requires control-plane");
        }
        return parseControl(root.get("control-plane"), sha256(json));
    }

    private static ControlPolicy parseControl(JsonNode control, String digest) {
        mapping(control, "control-plane");
        for (String key : names(control)) {
            if (UNSUPPORTED_CONTROL_KEYS.contains(key)) {
                throw new PolicyException("control-plane." + key + " is not supported by the Java backend yet");
            }
        }
        keys(control, "control-plane", Set.of("scope", "defaults", "campaigns"));
        ControlPolicy.Scope scope = new ControlPolicy.Scope(List.of(), List.of());
        if (control.has("scope")) {
            JsonNode node = control.get("scope");
            mapping(node, "control-plane.scope");
            keys(node, "control-plane.scope", Set.of("allowed-owners", "allowed-repositories"));
            scope = new ControlPolicy.Scope(
                    stringList(node.get("allowed-owners"), "allowed-owners", OWNER),
                    stringList(node.get("allowed-repositories"), "allowed-repositories", REPOSITORY));
        }
        ControlPolicy.Defaults defaults = new ControlPolicy.Defaults(null, null, null);
        if (control.has("defaults")) {
            JsonNode node = control.get("defaults");
            mapping(node, "control-plane.defaults");
            keys(node, "control-plane.defaults",
                    Set.of("mode", "max-repositories", "rollout-percent", "monthly-ai-credit-budget"));
            defaults = new ControlPolicy.Defaults(
                    mode(node, "control-plane.defaults.mode"),
                    integer(node, "max-repositories", 1, 1000, "control-plane.defaults"),
                    integer(node, "rollout-percent", 1, 100, "control-plane.defaults"));
            budget(node, "control-plane.defaults");
        }
        Map<String, ControlPolicy.Campaign> campaigns = new LinkedHashMap<>();
        if (control.has("campaigns")) {
            JsonNode node = control.get("campaigns");
            mapping(node, "control-plane.campaigns");
            for (String name : names(node)) {
                if (!SLUG.matcher(name).matches()) {
                    throw new PolicyException("malformed campaign identifier");
                }
                campaigns.put(name, parseCampaign(node.get(name), "control-plane.campaigns." + name));
            }
        }
        return new ControlPolicy(digest, scope, defaults, Map.copyOf(campaigns));
    }

    private static ControlPolicy.Campaign parseCampaign(JsonNode node, String path) {
        mapping(node, path);
        for (String key : names(node)) {
            if (UNSUPPORTED_CAMPAIGN_KEYS.contains(key)) {
                throw new PolicyException(path + "." + key + " is not supported by the Java backend yet");
            }
        }
        keys(node, path, Set.of("enabled", "mode", "max-repositories", "rollout-percent",
                "monthly-ai-credit-budget", "targets", "workers"));
        budget(node, path);
        Map<String, Mode> targets = new LinkedHashMap<>();
        if (node.has("targets")) {
            JsonNode targetNode = node.get("targets");
            mapping(targetNode, path + ".targets");
            for (String repository : names(targetNode)) {
                if (!REPOSITORY.matcher(repository).matches()) {
                    throw new PolicyException(path + ".targets has a malformed repository");
                }
                JsonNode policy = targetNode.get(repository);
                mapping(policy, path + ".targets." + repository);
                keys(policy, path + ".targets." + repository, Set.of("mode"));
                if (!policy.has("mode")) {
                    throw new PolicyException(path + ".targets." + repository + ".mode is required");
                }
                if (targets.put(repository.toLowerCase(Locale.ROOT),
                        mode(policy, path + ".targets." + repository + ".mode")) != null) {
                    throw new PolicyException(path + ".targets has a case-insensitive duplicate");
                }
            }
        }
        Map<String, ControlPolicy.Worker> workers = new LinkedHashMap<>();
        Set<String> workflows = new HashSet<>();
        if (node.has("workers")) {
            JsonNode workerNode = node.get("workers");
            mapping(workerNode, path + ".workers");
            for (String name : names(workerNode)) {
                if (!SLUG.matcher(name).matches()) {
                    throw new PolicyException("malformed worker identifier");
                }
                JsonNode worker = workerNode.get(name);
                String workerPath = path + ".workers." + name;
                mapping(worker, workerPath);
                keys(worker, workerPath, Set.of("workflow", "enabled", "max-mode"));
                JsonNode workflow = worker.get("workflow");
                if (workflow == null || !workflow.isTextual() || !SLUG.matcher(workflow.asText()).matches()) {
                    throw new PolicyException(workerPath + ".workflow must be a lowercase kebab-case slug");
                }
                if (!workflows.add(workflow.asText())) {
                    throw new PolicyException(path + " declares a workflow identity more than once");
                }
                workers.put(name, new ControlPolicy.Worker(
                        workflow.asText(), bool(worker, "enabled", workerPath), mode(worker, workerPath + ".max-mode", "max-mode")));
            }
        }
        return new ControlPolicy.Campaign(
                bool(node, "enabled", path),
                mode(node, path + ".mode"),
                integer(node, "max-repositories", 1, 1000, path),
                integer(node, "rollout-percent", 1, 100, path),
                Map.copyOf(targets),
                Map.copyOf(workers));
    }

    private static boolean bool(JsonNode node, String key, String path) {
        JsonNode value = node.get(key);
        if (value == null) {
            return true;
        }
        if (!value.isBoolean()) {
            throw new PolicyException(path + "." + key + " must be a boolean");
        }
        return value.asBoolean();
    }

    private static Mode mode(JsonNode node, String path) {
        return mode(node, path, "mode");
    }

    private static Mode mode(JsonNode node, String path, String key) {
        JsonNode value = node.get(key);
        if (value == null) {
            return null;
        }
        return Mode.parse(value.isTextual() ? value.asText() : null, path);
    }

    private static Integer integer(JsonNode node, String key, int min, int max, String path) {
        JsonNode value = node.get(key);
        if (value == null) {
            return null;
        }
        if (!value.isIntegralNumber() || !value.canConvertToInt() || value.asInt() < min || value.asInt() > max) {
            throw new PolicyException(path + "." + key + " must be an integer in " + min + ".." + max);
        }
        return value.asInt();
    }

    // Deprecated compatibility field: validated, then ignored (CAO-CFG §5.3, T-ARC-003).
    private static void budget(JsonNode node, String path) {
        JsonNode value = node.get("monthly-ai-credit-budget");
        if (value != null && (!value.isIntegralNumber() || !value.canConvertToInt() || value.asInt() < 0)) {
            throw new PolicyException(path + ".monthly-ai-credit-budget must be a non-negative integer");
        }
    }

    private static List<String> stringList(JsonNode node, String name, Pattern pattern) {
        if (node == null) {
            return List.of();
        }
        if (!node.isArray()) {
            throw new PolicyException(name + " must be an array");
        }
        List<String> values = new ArrayList<>();
        Set<String> seen = new HashSet<>();
        for (JsonNode item : node) {
            if (!item.isTextual() || !pattern.matcher(item.asText()).matches() || !seen.add(item.asText())) {
                throw new PolicyException(name + " must contain unique, well-formed entries; wildcards are not accepted");
            }
            values.add(item.asText());
        }
        return List.copyOf(values);
    }

    private static void mapping(JsonNode node, String path) {
        if (node == null || !node.isObject()) {
            throw new PolicyException(path + " must be a mapping");
        }
    }

    private static void keys(JsonNode node, String path, Set<String> allowed) {
        for (String key : names(node)) {
            if (!allowed.contains(key)) {
                throw new PolicyException("unknown property " + path + "." + key);
            }
        }
    }

    private static List<String> names(JsonNode node) {
        List<String> names = new ArrayList<>();
        Iterator<String> iterator = node.fieldNames();
        while (iterator.hasNext()) {
            names.add(iterator.next());
        }
        return names;
    }

    private static void rejectExpressions(JsonNode node) {
        if (node.isTextual() && node.asText().contains("${{")) {
            throw new PolicyException("GitHub Actions expressions are not allowed in policy");
        }
        for (String name : names(node)) {
            if (name.contains("${{")) {
                throw new PolicyException("GitHub Actions expressions are not allowed in policy");
            }
        }
        for (JsonNode child : node) {
            rejectExpressions(child);
        }
    }

    private static String decodeUtf8(byte[] json) {
        try {
            return StandardCharsets.UTF_8.newDecoder()
                    .onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .decode(ByteBuffer.wrap(json)).toString();
        } catch (CharacterCodingException e) {
            throw new PolicyException("policy must be UTF-8");
        }
    }

    private static String sha256(byte[] bytes) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
