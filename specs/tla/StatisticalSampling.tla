------------------------- MODULE StatisticalSampling -------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS Runs, Capacity, MaxRevision
ASSUME /\ Runs \subseteq Nat
       /\ Runs # {}
       /\ Capacity \in Nat \ {0}
       /\ MaxRevision \in Nat \ {0}

Min(a, b) == IF a < b THEN a ELSE b

\* A single stratum with identity-ordered priorities abstracts the sampler.
\* Hashing, date eligibility and multi-stratum allocation have executable tests.
Select(population) ==
    CHOOSE selected \in SUBSET population :
        /\ Cardinality(selected) = Min(Capacity, Cardinality(population))
        /\ \A x \in selected, y \in population \ selected : x < y

Children(selected) == {<<run, kind>> : run \in selected, kind \in {"tool", "audit"}}
Parents(selected) == {<<run, "workflow">> : run \in selected}
Empty == [available |-> FALSE, population |-> {}, selected |-> {},
          children |-> {}, parents |-> {}, sourceRevision |-> 0,
          complete |-> FALSE, clock |-> FALSE, weightValid |-> FALSE]
Bundle(population, revision, complete, clock, budget) ==
    IF ~clock \/ ~budget THEN [Empty EXCEPT !.sourceRevision = revision]
    ELSE [available |-> TRUE, population |-> population,
          selected |-> Select(population), children |-> Children(Select(population)),
          parents |-> Parents(Select(population)), sourceRevision |-> revision,
          complete |-> complete, clock |-> clock, weightValid |-> complete]

VARIABLES canonical, revision, published, staged, stagedExact, phase, previous,
          exactAtStage, restarted, observed, observedFrame, observedFilter
vars == <<canonical, revision, published, staged, stagedExact, phase, previous,
          exactAtStage, restarted, observed, observedFrame, observedFilter>>

Init == /\ canonical \in SUBSET Runs
        /\ revision = 0
        /\ published = Empty
        /\ staged = Empty
        /\ stagedExact = {}
        /\ phase = "idle"
        /\ previous = Empty
        /\ exactAtStage = {}
        /\ restarted = FALSE
        /\ observed = {}
        /\ observedFrame = Empty
        /\ observedFilter = {}

\* Complete publication and overlay refresh take one immutable recorded snapshot.
Stage(population, complete, clock, budget) ==
    /\ phase = "idle"
    /\ revision < MaxRevision
    /\ phase' = "staged"
    /\ staged' = Bundle(population, revision + 1, complete, clock, budget)
    /\ stagedExact' = population
    /\ previous' = published
    /\ exactAtStage' = canonical
    /\ UNCHANGED <<canonical, revision, published, restarted,
                   observed, observedFrame, observedFilter>>

Commit ==
    /\ phase = "staged"
    /\ canonical' = stagedExact
    /\ revision' = revision + 1
    /\ published' = staged
    /\ phase' = "idle"
    /\ UNCHANGED <<staged, stagedExact, previous, exactAtStage, restarted,
                   observed, observedFrame, observedFilter>>

Abort ==
    /\ phase = "staged"
    /\ phase' = "idle"
    /\ UNCHANGED <<canonical, revision, published, staged, stagedExact, previous,
                   exactAtStage, restarted, observed, observedFrame, observedFilter>>

\* PostgreSQL incremental writes/retention change exact evidence, not bundles.
Incremental(population) ==
    /\ phase = "idle"
    /\ revision < MaxRevision
    /\ canonical' = population
    /\ revision' = revision + 1
    /\ UNCHANGED <<published, staged, stagedExact, phase, previous, exactAtStage, restarted,
                   observed, observedFrame, observedFilter>>

Restart ==
    /\ ~restarted
    /\ restarted' = TRUE
    /\ UNCHANGED <<canonical, revision, published, staged, stagedExact, phase, previous,
                   exactAtStage, observed, observedFrame, observedFilter>>

Read(filter) ==
    /\ observed' = published.selected \cap filter
    /\ observedFrame' = published
    /\ observedFilter' = filter
    /\ UNCHANGED <<canonical, revision, published, staged, stagedExact, phase, previous,
                   exactAtStage, restarted>>

Next == \/ \E population \in SUBSET Runs, complete \in BOOLEAN,
              clock \in BOOLEAN, budget \in BOOLEAN :
                  Stage(population, complete, clock, budget)
        \/ Commit
        \/ Abort
        \/ \E population \in SUBSET Runs : Incremental(population)
        \/ Restart
        \/ \E filter \in SUBSET Runs : Read(filter)

TypeOK == /\ canonical \subseteq Runs
          /\ revision \in 0..MaxRevision
          /\ phase \in {"idle", "staged"}
          /\ restarted \in BOOLEAN
          /\ published.selected \subseteq published.population
          /\ published.population \subseteq Runs
          /\ published.sourceRevision <= revision

WholeBundles == /\ published.children = Children(published.selected)
                /\ published.parents = Parents(published.selected)
Bounded == Cardinality(published.selected) <= Capacity
NoPartialPublication ==
    phase = "staged" => published = previous /\ canonical = exactAtStage
UnavailableIsEmpty ==
    ~published.available => /\ published.population = {}
                            /\ published.selected = {}
                            /\ ~published.weightValid
HonestWeights ==
    published.weightValid => published.available /\ published.complete /\ published.clock
FrozenSelection ==
    published.available => published.selected = Select(published.population)
SelectionBeforeFilter ==
    /\ observed = observedFrame.selected \cap observedFilter
    /\ observed \subseteq observedFrame.population
FrozenAcrossIncremental ==
    [][(revision' # revision /\ phase = "idle") => published' = published]_vars
RestartPreservesEvidence ==
    [][(~restarted /\ restarted') =>
        UNCHANGED <<canonical, revision, published>>]_vars
ExactPublicationIndependent ==
    [][(phase = "staged" /\ revision' # revision) =>
        canonical' = stagedExact]_vars
AbortPreservesPublication ==
    [][(phase = "staged" /\ phase' = "idle" /\ revision' = revision) =>
        UNCHANGED <<canonical, published>>]_vars

Spec == Init /\ [][Next]_vars
=============================================================================
