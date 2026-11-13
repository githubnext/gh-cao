------------------------- MODULE StatisticalSampling -------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS Runs, Capacity, MaxRevision, MaxTime, RefreshInterval
ASSUME /\ Runs \subseteq Nat
       /\ Runs # {}
       /\ Capacity \in Nat \ {0}
       /\ MaxRevision \in Nat \ {0}
       /\ MaxTime \in Nat \ {0}
       /\ RefreshInterval \in Nat \ {0}

Min(a, b) == IF a < b THEN a ELSE b
Select(population) ==
    CHOOSE selected \in SUBSET population :
        /\ Cardinality(selected) = Min(Capacity, Cardinality(population))
        /\ \A x \in selected, y \in population \ selected : x < y

Parents(selected) == {<<run, "workflow">> : run \in selected}
Empty == [available |-> FALSE, population |-> {}, selected |-> {},
          parents |-> {}, sourceRevision |-> 0,
          complete |-> FALSE, clock |-> FALSE, weightValid |-> FALSE]
Summary(population, revision, complete, clock, budget) ==
    IF ~clock \/ ~budget THEN [Empty EXCEPT !.sourceRevision = revision]
    ELSE [available |-> TRUE, population |-> population,
          selected |-> Select(population),
          parents |-> Parents(Select(population)), sourceRevision |-> revision,
          complete |-> complete, clock |-> clock, weightValid |-> complete]

VARIABLES canonical, revision, published, staged, stagedExact, phase, previous,
          exactAtStage, restarted, observed, observedFrame, observedFilter,
          now, lastAttempt, attempted
vars == <<canonical, revision, published, staged, stagedExact, phase, previous,
          exactAtStage, restarted, observed, observedFrame, observedFilter,
          now, lastAttempt, attempted>>

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
        /\ now = 0
        /\ lastAttempt = 0
        /\ attempted = FALSE

\* Canonical publication never samples, waits for sampling, or changes the cache.
Stage(population) ==
    /\ phase = "idle"
    /\ revision < MaxRevision
    /\ phase' = "ingesting"
    /\ stagedExact' = population
    /\ previous' = published
    /\ exactAtStage' = canonical
    /\ UNCHANGED <<canonical, revision, published, staged, restarted,
                   observed, observedFrame, observedFilter, now, lastAttempt, attempted>>

Commit ==
    /\ phase = "ingesting"
    /\ canonical' = stagedExact
    /\ revision' = revision + 1
    /\ phase' = "idle"
    /\ UNCHANGED <<published, staged, stagedExact, previous, exactAtStage,
                   restarted, observed, observedFrame, observedFilter,
                   now, lastAttempt, attempted>>

\* A demand-driven attempt is fenced independently of revisions and cache contents.
StartSample(complete, clock, budget) ==
    /\ phase = "idle"
    /\ ~attempted \/ now >= lastAttempt + RefreshInterval
    /\ ~published.available \/ published.sourceRevision # revision
    /\ phase' = "sampling"
    /\ staged' = Summary(canonical, revision, complete, clock, budget)
    /\ previous' = published
    /\ exactAtStage' = canonical
    /\ lastAttempt' = now
    /\ attempted' = TRUE
    /\ UNCHANGED <<canonical, revision, published, stagedExact, restarted,
                   observed, observedFrame, observedFilter, now>>

CommitSample ==
    /\ phase = "sampling"
    /\ published' = staged
    /\ phase' = "idle"
    /\ UNCHANGED <<canonical, revision, staged, stagedExact, previous, exactAtStage,
                   restarted, observed, observedFrame, observedFilter,
                   now, lastAttempt, attempted>>

Abort ==
    /\ phase \in {"ingesting", "sampling"}
    /\ phase' = "idle"
    /\ UNCHANGED <<canonical, revision, published, staged, stagedExact, previous,
                   exactAtStage, restarted, observed, observedFrame, observedFilter,
                   now, lastAttempt, attempted>>

Withdraw ==
    /\ phase = "idle"
    /\ published # Empty
    /\ published' = Empty
    /\ UNCHANGED <<canonical, revision, staged, stagedExact, phase, previous,
                   exactAtStage, restarted, observed, observedFrame, observedFilter,
                   now, lastAttempt, attempted>>

\* The documented process-local cache and attempt fence are disposable on restart.
Restart ==
    /\ phase = "idle"
    /\ ~restarted
    /\ restarted' = TRUE
    /\ published' = Empty
    /\ attempted' = FALSE
    /\ lastAttempt' = 0
    /\ UNCHANGED <<canonical, revision, staged, stagedExact, phase, previous,
                   exactAtStage, observed, observedFrame, observedFilter, now>>

Tick ==
    /\ now < MaxTime
    /\ now' = now + 1
    /\ UNCHANGED <<canonical, revision, published, staged, stagedExact, phase, previous,
                   exactAtStage, restarted, observed, observedFrame, observedFilter,
                   lastAttempt, attempted>>

Read(filter) ==
    /\ observed' = published.selected \cap filter
    /\ observedFrame' = published
    /\ observedFilter' = filter
    /\ UNCHANGED <<canonical, revision, published, staged, stagedExact, phase, previous,
                   exactAtStage, restarted, now, lastAttempt, attempted>>

Next == \/ \E population \in SUBSET Runs : Stage(population)
        \/ Commit
        \/ \E complete \in BOOLEAN, clock \in BOOLEAN, budget \in BOOLEAN :
            StartSample(complete, clock, budget)
        \/ CommitSample
        \/ Abort
        \/ Withdraw
        \/ Restart
        \/ Tick
        \/ \E filter \in SUBSET Runs : Read(filter)

TypeOK == /\ canonical \subseteq Runs
          /\ revision \in 0..MaxRevision
          /\ phase \in {"idle", "ingesting", "sampling"}
          /\ restarted \in BOOLEAN
          /\ published.selected \subseteq published.population
          /\ published.population \subseteq Runs
          /\ published.sourceRevision <= revision
          /\ now \in 0..MaxTime
          /\ lastAttempt <= now
          /\ attempted \in BOOLEAN
WholeSummaries == published.parents = Parents(published.selected)
Bounded == Cardinality(published.selected) <= Capacity
NoPartialPublication ==
    phase # "idle" => published = previous /\ canonical = exactAtStage
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
IngestionDoesNotSample ==
    [][(phase = "ingesting") => published' = published]_vars
RefreshThrottled ==
    [][(phase' = "sampling" /\ phase # "sampling" /\ attempted) =>
        now >= lastAttempt + RefreshInterval]_vars
RestartDiscardsEvidence ==
    [][(~restarted /\ restarted') => published' = Empty]_vars
ExactPublicationIndependent ==
    [][(phase = "ingesting" /\ revision' # revision) =>
        canonical' = stagedExact]_vars
AbortPreservesPublication ==
    [][(phase # "idle" /\ phase' = "idle" /\ revision' = revision /\ published' # staged) =>
        UNCHANGED <<canonical, published>>]_vars

Spec == Init /\ [][Next]_vars
=============================================================================
