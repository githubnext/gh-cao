package org.cao.backend.dashboard;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;

/**
 * Validates one Dashboard Language document and prints a JSON report; exit 0 valid, 1 invalid, 2 usage or
 * I/O failure. It reads only the named local file and executes nothing.
 */
public final class ValidateMain {
    private ValidateMain() { }

    public static void main(String[] args) throws IOException {
        if (args.length != 1) {
            System.err.println("usage: ValidateMain <dashboard.yml>");
            System.exit(2);
            return;
        }
        ValidationResult result = DashboardValidator.validate(Files.readAllBytes(Path.of(args[0])));
        ObjectMapper mapper = new ObjectMapper();
        ObjectNode out = mapper.createObjectNode();
        out.put("ok", result.ok());
        ArrayNode errors = out.putArray("errors");
        result.errors().forEach(e -> errors.addObject()
                .put("code", e.code()).put("message", e.message()).put("path", e.path()));
        ArrayNode limits = out.putArray("limitations");
        result.limitations().forEach(limits::add);
        ArrayNode notImplemented = out.putArray("notImplemented");
        DashboardValidator.notImplemented().forEach(notImplemented::add);
        System.out.println(mapper.writerWithDefaultPrettyPrinter().writeValueAsString(out));
        System.exit(result.ok() ? 0 : 1);
    }
}
