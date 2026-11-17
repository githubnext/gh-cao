package org.cao.backend.policy;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Role-independent authority envelope (specs/control-architecture.md §4.6, CAO-EXE-006): v1 grants pair
 * each target with a mode and an output destination and are bound to the verified execution context.
 * An envelope never grants gh-aw capabilities.
 */
public record AuthorityEnvelope(
        int version,
        String operation,
        ExecutionBinding binding,
        int maxTargets,
        List<Grant> grants,
        List<String> requirements,
        boolean delegation) {

    public static final int VERSION = 1;
    public static final List<String> REQUIRED_CONTROLS = List.of("policy-revision", "target-scope", "output-routing");
    private static final Pattern OPERATION = Pattern.compile("^[a-z0-9][a-z0-9:/-]{0,255}$");

    /** One target paired with its mode and output destination. */
    public record Grant(String target, Mode mode, String destination) { }

    public AuthorityEnvelope {
        grants = List.copyOf(grants);
        requirements = List.copyOf(requirements);
    }

    /** Validates structure and compares the envelope's binding with the expected execution context. */
    public AuthorityEnvelope validate(ExecutionBinding expected, List<String> supportedControls) {
        if (version != VERSION) {
            throw new PolicyException("unsupported authority envelope version");
        }
        if (operation == null || !OPERATION.matcher(operation).matches()) {
            throw new PolicyException("invalid envelope operation identity");
        }
        if (!binding.equals(expected)) {
            throw new PolicyException("envelope binding mismatch");
        }
        if (maxTargets < 1 || maxTargets > 1000) {
            throw new PolicyException("max_repositories must be an integer in 1..1000");
        }
        if (grants.isEmpty() || grants.size() > maxTargets) {
            throw new PolicyException("envelope grants exceed admitted target limit");
        }
        Set<String> names = new HashSet<>();
        for (Grant grant : grants) {
            PolicyResolver.requireRepository(grant.target(), "envelope target");
            PolicyResolver.requireRepository(grant.destination(), "envelope destination");
            if (grant.mode() == null) {
                throw new PolicyException("grant mode must be review or live");
            }
            if (!names.add(grant.target().toLowerCase(Locale.ROOT))) {
                throw new PolicyException("duplicate envelope target");
            }
        }
        if (new HashSet<>(requirements).size() != requirements.size()
                || !requirements.containsAll(REQUIRED_CONTROLS)
                || !supportedControls.containsAll(requirements)) {
            throw new PolicyException("unsupported or missing required enforcement control");
        }
        if (delegation) {
            throw new PolicyException("direct operation delegation is not supported");
        }
        return this;
    }

    /**
     * A child may only narrow its parent. Both values must already be verified by the receiving
     * integration; this comparison is not provenance verification or authorization.
     */
    public static void requireNarrowing(AuthorityEnvelope parent, AuthorityEnvelope child, List<String> controls) {
        parent.validate(parent.binding(), controls);
        child.validate(child.binding(), controls);
        if (!parent.operation().equals(child.operation()) || parent.maxTargets() < child.maxTargets()
                || !parent.binding().repository().equals(child.binding().repository())
                || !parent.binding().policySha().equals(child.binding().policySha())) {
            throw new PolicyException("handoff exceeds parent authority");
        }
        List<Grant> ceilings = new ArrayList<>(parent.grants());
        for (Grant grant : child.grants()) {
            Grant ceiling = ceilings.stream()
                    .filter(candidate -> candidate.target().equalsIgnoreCase(grant.target()))
                    .findFirst()
                    .orElseThrow(() -> new PolicyException("handoff widens a scoped grant"));
            if (grant.mode().ordinal() > ceiling.mode().ordinal()
                    || !ceiling.destination().equalsIgnoreCase(grant.destination())) {
                throw new PolicyException("handoff widens a scoped grant");
            }
        }
    }
}
