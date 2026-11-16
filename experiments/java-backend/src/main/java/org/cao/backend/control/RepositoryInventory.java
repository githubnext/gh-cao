package org.cao.backend.control;

import java.util.List;

/**
 * Domain contract for reading exactly enrolled repositories. Implementations own transport and
 * credentials; they must return one record per requested repository or throw
 * {@link InventoryUnavailableException}, never partial or guessed results.
 */
public interface RepositoryInventory {
    List<RepositoryRecord> load(List<String> exactRepositories);
}
