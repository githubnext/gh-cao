package org.cao.backend.operational;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

class AuditRulesTest {

    private static AuditEvent event(String name, String outcome, String actor, Map<String, String> a) {
        return AuditEvent.of(name, outcome, actor, a);
    }

    @Test
    void acceptsBoundedEvent() {
        assertDoesNotThrow(() -> AuditRules.validate(
            event("admin.rebuild.admitted", "success", "octocat", Map.of("k", "v"))));
    }

    @Test
    void rejectsBadNamesOutcomesActorsAndAttributes() {
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event(" ", "success", "", Map.of())));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event(null, "success", "", Map.of())));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "ok", "", Map.of())));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", null, "", Map.of())));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "success", "a\0b", Map.of())));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "success", "x".repeat(1025), Map.of())));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "success", "", Map.of("k", "a\0"))));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "success", "", Map.of("k", "x".repeat(4097)))));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "success", "", Map.of(" ", "v"))));
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "success", "", Map.of("k", "\uD800"))));
    }

    @Test
    void rejectsTooManyAttributes() {
        Map<String, String> many = new HashMap<>();
        for (int i = 0; i < 65; i++) {
            many.put("k" + i, "v");
        }
        assertThrows(AuditInputException.class,
            () -> AuditRules.validate(event("e", "success", "", many)));
    }
}
