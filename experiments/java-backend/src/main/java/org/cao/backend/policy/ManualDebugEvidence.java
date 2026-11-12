package org.cao.backend.policy;

import java.util.Map;

/**
 * Authoritative facts about a launch, supplied by a trusted event reader. This class performs no
 * GitHub calls; null or blank members mean the evidence was unavailable and admission fails closed.
 *
 * @param eventName triggering event name; only {@code workflow_dispatch} is admissible
 * @param actor human sender of the event
 * @param actorType account type of the actor, {@code User} for humans
 * @param triggeringActor actor that triggered this run attempt (differs from actor on a foreign rerun)
 * @param actorPermission current control-repository permission of the actor: read, triage, write, maintain, admin
 * @param inputs the workflow_dispatch inputs exactly as received
 */
public record ManualDebugEvidence(
        String eventName,
        String actor,
        String actorType,
        String triggeringActor,
        String actorPermission,
        Map<String, String> inputs) { }
