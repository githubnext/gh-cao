package org.cao.backend.control;

import java.util.List;

/**
 * Domain contract for control-repository workflow evidence: the workflow inventory and the
 * importing workflow's source at an exact commit. Failures throw {@link InventoryUnavailableException}.
 */
public interface WorkflowCatalog {
    List<WorkflowEntry> list();

    String source(String path, String sha);
}
