package org.cao.backend.http;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.Set;
import org.cao.backend.policy.PolicyException;

/**
 * Resolves {@code control-plane.web.host.cors} from a {@code cao.json} document
 * (specs/server-cors.md §3.1). Only the structure on the path to {@code cors} is validated here:
 * the rest of the host document ({@code target}, {@code operational-store}) is not interpreted
 * by this backend yet, so this loader must not be read as full host-policy validation.
 */
public final class HostPolicy {
    private static final ObjectMapper MAPPER = new ObjectMapper()
            .enable(JsonParser.Feature.STRICT_DUPLICATE_DETECTION)
            .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS);
    private static final Set<String> WEB_KEYS = Set.of("experimental", "favicon", "host");
    private static final Set<String> HOST_KEYS = Set.of("target", "operational-store", "cors");

    private HostPolicy() { }

    /** Absent {@code web}, {@code host} or {@code cors} members yield the same-origin default. */
    public static CorsPolicy loadCors(byte[] json) {
        JsonNode root = parse(json);
        if (!root.isObject()) {
            throw new PolicyException("policy root must be an object");
        }
        JsonNode version = root.get("version");
        if (version == null || !version.isIntegralNumber() || version.asInt() != 1) {
            throw new PolicyException("policy.version must be integer 1");
        }
        JsonNode control = root.get("control-plane");
        if (control == null || !control.isObject()) {
            throw new PolicyException("policy requires control-plane");
        }
        JsonNode web = control.get("web");
        if (web == null) {
            return CorsPolicy.sameOrigin();
        }
        requireKeys(web, "control-plane.web", WEB_KEYS);
        JsonNode host = web.get("host");
        if (host == null) {
            return CorsPolicy.sameOrigin();
        }
        requireKeys(host, "control-plane.web.host", HOST_KEYS);
        JsonNode cors = host.get("cors");
        return cors == null ? CorsPolicy.sameOrigin() : CorsPolicy.fromMember(cors, "control-plane.web.host.cors");
    }

    private static void requireKeys(JsonNode node, String path, Set<String> allowed) {
        if (!node.isObject()) {
            throw new PolicyException(path + " must be a mapping");
        }
        node.fieldNames().forEachRemaining(name -> {
            if (!allowed.contains(name)) {
                throw new PolicyException("unknown property " + path + "." + name);
            }
        });
    }

    private static JsonNode parse(byte[] json) {
        String text;
        try {
            text = StandardCharsets.UTF_8.newDecoder()
                    .onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .decode(ByteBuffer.wrap(json)).toString();
        } catch (CharacterCodingException e) {
            throw new PolicyException("policy must be UTF-8");
        }
        if (text.contains("${{")) {
            throw new PolicyException("GitHub Actions expressions are not allowed in policy");
        }
        try {
            JsonNode root = MAPPER.readTree(text);
            if (root == null) {
                throw new PolicyException("policy is empty");
            }
            return root;
        } catch (JsonProcessingException e) {
            throw new PolicyException("policy is not valid JSON without duplicate keys");
        }
    }
}
