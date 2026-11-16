package org.cao.backend.control;

import static org.junit.jupiter.api.Assertions.assertEquals;

import java.util.List;
import org.junit.jupiter.api.Test;

class DispatchWorkflowsTest {
    @Test
    void readsInlineAndBlockListsOnceEach() {
        assertEquals(List.of("a-b", "c"), DispatchWorkflows.parse(
                "---\nsafe-outputs:\n  dispatch-workflow:\n    workflows: ['a-b', \"c\", a-b]\n---\nbody"));
        assertEquals(List.of("x", "y"), DispatchWorkflows.parse(
                "---\nsafe-outputs:\n  dispatch-workflow:\n    max: 2\n    workflows:\n      - x\n      - 'y'\n  noop:\n---\n"));
    }

    @Test
    void ignoresUndeclaredOrNonFrontmatterContent() {
        assertEquals(List.of(), DispatchWorkflows.parse("safe-outputs:\n  dispatch-workflow:\n    workflows: [a]\n"));
        assertEquals(List.of(), DispatchWorkflows.parse("---\nname: x\n---\n    workflows: [a]\n"));
        assertEquals(List.of(), DispatchWorkflows.parse("---\nother:\n  dispatch-workflow:\n    workflows: [a]\n---\n"));
        assertEquals(List.of(), DispatchWorkflows.parse(""));
    }
}
