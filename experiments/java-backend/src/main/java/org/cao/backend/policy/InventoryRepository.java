package org.cao.backend.policy;

import java.util.regex.Pattern;

/**
 * Repository metadata evidence for inventory partitioning. Field order matches the metadata the
 * Node.js control resolver serializes into the inventory version digest.
 *
 * @param pushedAt nullable ISO-8601 text as supplied by the evidence source
 * @param defaultBranch nullable
 */
public record InventoryRepository(
        long id,
        String fullName,
        boolean archived,
        boolean disabled,
        boolean isPrivate,
        String pushedAt,
        String defaultBranch) {
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");

    public InventoryRepository {
        if (id <= 0 || fullName == null || !REPOSITORY.matcher(fullName).matches()) {
            throw new PolicyException("inventory repository metadata is malformed");
        }
    }
}
