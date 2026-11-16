package org.cao.backend.control;

import java.util.regex.Pattern;

/**
 * Repository identity and state as observed by an inventory source. Validation mirrors the response
 * checks the production inventory applies (positive id, owner/repository name, boolean flags).
 */
public record RepositoryRecord(
        long id,
        String fullName,
        boolean archived,
        boolean disabled,
        boolean isPrivate,
        String pushedAt,
        String defaultBranch) {
    private static final Pattern NAME = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final long MAX_SAFE_INTEGER = 9_007_199_254_740_991L;

    public RepositoryRecord {
        if (id <= 0 || id > MAX_SAFE_INTEGER || fullName == null || !NAME.matcher(fullName).matches()) {
            throw new InventoryUnavailableException("inventory-response-invalid",
                    "Repository metadata is malformed or does not match the enrolled repository identity.");
        }
    }
}
