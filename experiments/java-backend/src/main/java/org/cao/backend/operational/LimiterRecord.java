package org.cao.backend.operational;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.time.OffsetDateTime;
import org.cao.backend.ratelimit.TokenBucket;

/**
 * Stored form of a limiter bucket: the bytes Go's {@code json.Marshal} produces for
 * {@code tokenBucket{Tokens float64, At time.Time}}, so either implementation continues a bucket
 * the other wrote.
 */
final class LimiterRecord {
    private static final ObjectMapper JSON = new ObjectMapper();

    private LimiterRecord() { }

    static byte[] encode(TokenBucket bucket) {
        String text = "{\"Tokens\":" + goFloat(bucket.tokens()) + ",\"At\":\""
            + CanonicalAudit.time(bucket.at()) + "\"}";
        return text.getBytes(StandardCharsets.UTF_8);
    }

    static TokenBucket decode(byte[] value) {
        try {
            JsonNode node = JSON.readTree(value);
            JsonNode tokens = node.get("Tokens");
            JsonNode at = node.get("At");
            if (tokens == null || !tokens.isNumber() || at == null || !at.isTextual()
                || tokens.asDouble() < 0 || Double.isNaN(tokens.asDouble())
                || Double.isInfinite(tokens.asDouble())) {
                throw new OperationalStoreException("corrupt operational record");
            }
            return new TokenBucket(tokens.asDouble(), OffsetDateTime.parse(at.asText()).toInstant());
        } catch (IOException | java.time.format.DateTimeParseException e) {
            throw new OperationalStoreException("corrupt operational record", e);
        }
    }

    /**
     * Go's float64 JSON encoding: shortest round-trip digits, plain notation unless the magnitude is
     * below 1e-6 or at least 1e21, where exponent notation drops the zero of a one-digit negative
     * exponent ({@code 1e-7}, never {@code 1e-07}).
     */
    static String goFloat(double value) {
        if (value == 0) {
            return 1 / value < 0 ? "-0" : "0";
        }
        BigDecimal decimal = new BigDecimal(Double.toString(value)).stripTrailingZeros();
        double magnitude = Math.abs(value);
        if (magnitude >= 1e-6 && magnitude < 1e21) {
            return decimal.toPlainString();
        }
        String digits = decimal.unscaledValue().abs().toString();
        int exponent = digits.length() - 1 - decimal.scale();
        StringBuilder out = new StringBuilder();
        if (decimal.signum() < 0) {
            out.append('-');
        }
        out.append(digits.charAt(0));
        if (digits.length() > 1) {
            out.append('.').append(digits, 1, digits.length());
        }
        out.append('e').append(exponent < 0 ? '-' : '+');
        int abs = Math.abs(exponent);
        if (exponent > 0 && abs < 10) {
            out.append('0');
        }
        return out.append(abs).toString();
    }
}
