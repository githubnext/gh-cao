package org.cao.backend.control;

/** A control-repository workflow as reported by the workflow catalog. */
public record WorkflowEntry(long id, String name, String path, String state) { }
