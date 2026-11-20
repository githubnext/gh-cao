package org.cao.backend.policy;

import java.util.regex.Pattern;

/**
 * Repository metadata evidence supplied by an inventory adapter. The policy kernel performs no discovery.
 *
 * @param pushedAt nullable ISO-8601 timestamp, passed through verbatim into the inventory digest
 * @param defaultBranch nullable
 */
public record RepositoryRecord(
        long id,
        String fullName,
        boolean archived,
        boolean disabled,
        boolean isPrivate,
        String pushedAt,
        String defaultBranch) {
    private static final Pattern FULL_NAME = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");

    public RepositoryRecord {
        if (id <= 0 || fullName == null || !FULL_NAME.matcher(fullName).matches()) {
            throw new PolicyException("repository metadata is malformed");
        }
    }
}
