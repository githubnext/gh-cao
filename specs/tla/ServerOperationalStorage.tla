----------------------- MODULE ServerOperationalStorage -----------------------
EXTENDS Naturals, Integers, FiniteSets

CONSTANTS Backend, Jobs, Capacity, MaxRestarts

ASSUME /\ Backend \in {"postgres", "memory"}
       /\ Jobs # {}
       /\ Capacity \in Nat \ {0}
       /\ MaxRestarts \in Nat

VARIABLES mode, topology, phase, epoch, opened, bootstrapReady,
          markers, ready, leases, completed, active, sessions, checkpointPages,
          evidence, admissionAuthorized, history

vars == <<mode, topology, phase, epoch, opened, bootstrapReady,
          markers, ready, leases, completed, active, sessions, checkpointPages,
          evidence, admissionAuthorized, history>>

OperationalState == <<markers, ready, leases, completed, sessions, checkpointPages>>
Remember == history' = [phase |-> phase, evidence |-> evidence, protected |-> OperationalState]

GoodTopology == [oauth |-> TRUE, https |-> TRUE, listener |-> "process",
                 singleProcess |-> TRUE, replicas |-> 1, allowVolatile |-> TRUE,
                 colocated |-> TRUE, workers |-> 1]

Topologies == {GoodTopology,
               [GoodTopology EXCEPT !.oauth = FALSE],
               [GoodTopology EXCEPT !.https = FALSE],
               [GoodTopology EXCEPT !.listener = "platform"],
               [GoodTopology EXCEPT !.singleProcess = FALSE],
               [GoodTopology EXCEPT !.replicas = 2],
               [GoodTopology EXCEPT !.allowVolatile = FALSE],
               [GoodTopology EXCEPT !.colocated = FALSE],
               [GoodTopology EXCEPT !.workers = 0]}

HostAllowed == /\ topology.oauth
               /\ topology.https
               /\ topology.listener = "process"

MemoryAllowed == /\ topology.singleProcess
                 /\ topology.replicas = 1
                 /\ topology.allowVolatile
                 /\ topology.colocated
                 /\ topology.workers > 0

SelectionAllowed == /\ mode = Backend
                    /\ HostAllowed
                    /\ (Backend = "memory" => MemoryAllowed)

Leased == {job \in Jobs : leases[job] # -1}
Pending == ready \cup Leased
Serving == phase = "running" /\ opened /\ bootstrapReady

EmptyOperationalState == /\ markers' = {}
                         /\ ready' = {}
                         /\ leases' = [job \in Jobs |-> -1]
                         /\ completed' = {}
                         /\ sessions' = {}
                         /\ checkpointPages' = {}

Init == /\ mode \in {"browser", "postgres", "memory"}
        /\ topology \in Topologies
        /\ phase = "cold"
        /\ epoch = 0
        /\ opened = FALSE
        /\ bootstrapReady = FALSE
        /\ markers = {}
        /\ ready = {}
        /\ leases = [job \in Jobs |-> -1]
        /\ completed = {}
        /\ active = {}
        /\ sessions = {}
        /\ checkpointPages = {}
        /\ evidence = {}
        /\ admissionAuthorized = TRUE
        /\ history = [phase |-> phase, evidence |-> evidence, protected |-> OperationalState]

Start == /\ phase = "cold"
         /\ phase' = (IF mode = "browser" THEN "browser"
                      ELSE IF ~SelectionAllowed THEN "rejected"
                      ELSE IF Backend = "memory" THEN "recovering" ELSE "running")
         /\ opened' = (mode # "browser" /\ SelectionAllowed)
         /\ bootstrapReady' = (mode # "browser" /\ SelectionAllowed /\ Backend = "postgres")
         /\ UNCHANGED <<mode, topology, epoch, OperationalState,
                        active, evidence, admissionAuthorized>>

\* Memory bootstrap abstracts complete fresh, authorized enumeration. Partial
\* enumeration cannot execute this transition or authorize scope-dependent work.
Bootstrap == /\ phase = "recovering"
             /\ opened
             /\ phase' = "running"
             /\ bootstrapReady' = TRUE
             /\ UNCHANGED <<mode, topology, epoch, opened, OperationalState,
                            active, evidence, admissionAuthorized>>

Admit(job, backfill) == /\ Serving
                       /\ job \in Jobs \ markers
                       /\ Cardinality(Pending) < Capacity
                       /\ markers' = markers \cup {job}
                       /\ ready' = ready \cup {job}
                       /\ checkpointPages' = (IF backfill
                                              THEN checkpointPages \cup {job}
                                              ELSE checkpointPages)
                       /\ admissionAuthorized' = Serving
                       /\ UNCHANGED <<mode, topology, phase, epoch, opened,
                                      bootstrapReady, leases, completed, active,
                                      sessions, evidence>>

Lease(job) == /\ Serving
              /\ job \in ready
              /\ ready' = ready \ {job}
              /\ leases' = [leases EXCEPT ![job] = epoch]
              /\ active' = active \cup {job}
              /\ UNCHANGED <<mode, topology, phase, epoch, opened, bootstrapReady,
                             markers, completed, sessions, checkpointPages,
                             evidence, admissionAuthorized>>

Complete(job, owner) == /\ Serving
                        /\ job \in active
                        /\ leases[job] = owner
                        /\ owner = epoch
                        /\ leases' = [leases EXCEPT ![job] = -1]
                        /\ active' = active \ {job}
                        /\ completed' = completed \cup {job}
                        /\ evidence' = evidence \cup {job}
                        /\ UNCHANGED <<mode, topology, phase, epoch, opened,
                                       bootstrapReady, markers, ready, sessions,
                                       checkpointPages, admissionAuthorized>>

ReclaimExpired(job) == /\ Serving
                       /\ job \in Leased \ active
                       /\ leases[job] # epoch
                       /\ ready' = ready \cup {job}
                       /\ leases' = [leases EXCEPT ![job] = -1]
                       /\ UNCHANGED <<mode, topology, phase, epoch, opened,
                                      bootstrapReady, markers, completed, active,
                                      sessions, checkpointPages, evidence,
                                      admissionAuthorized>>

IssueSession == /\ Serving
                /\ sessions' = sessions \cup {epoch}
                /\ UNCHANGED <<mode, topology, phase, epoch, opened, bootstrapReady,
                               markers, ready, leases, completed, active,
                               checkpointPages, evidence, admissionAuthorized>>

Logout(session) == /\ Serving
                   /\ session \in sessions
                   /\ sessions' = sessions \ {session}
                   /\ UNCHANGED <<mode, topology, phase, epoch, opened,
                                  bootstrapReady, markers, ready, leases,
                                  completed, active, checkpointPages, evidence,
                                  admissionAuthorized>>

Stop == /\ phase \in {"running", "recovering"}
        /\ phase' = "stopping"
        /\ bootstrapReady' = FALSE
        /\ UNCHANGED <<mode, topology, epoch, opened, OperationalState, active,
                       evidence, admissionAuthorized>>

JoinWorkers == /\ phase = "stopping"
               /\ active # {}
               /\ active' = {}
               /\ UNCHANGED <<mode, topology, phase, epoch, opened, bootstrapReady,
                              OperationalState, evidence, admissionAuthorized>>

Close == /\ phase = "stopping"
         /\ active = {}
         /\ phase' = "closed"
         /\ opened' = FALSE
         /\ (IF Backend = "memory" THEN EmptyOperationalState
             ELSE UNCHANGED OperationalState)
         /\ UNCHANGED <<mode, topology, epoch, bootstrapReady, active, evidence,
                        admissionAuthorized>>

StopBrowser == /\ phase = "browser"
               /\ phase' = "closed"
               /\ UNCHANGED <<mode, topology, epoch, opened, bootstrapReady,
                              OperationalState, active, evidence,
                              admissionAuthorized>>

Restart == /\ phase = "closed"
           /\ epoch < MaxRestarts
           /\ epoch' = epoch + 1
           /\ phase' = "cold"
           /\ UNCHANGED <<mode, topology, opened, bootstrapReady, OperationalState,
                          active, evidence, admissionAuthorized>>

Next == /\ (\/ Start
            \/ Bootstrap
            \/ \E job \in Jobs, backfill \in BOOLEAN : Admit(job, backfill)
            \/ \E job \in Jobs : Lease(job)
            \/ \E job \in Jobs, owner \in 0..MaxRestarts : Complete(job, owner)
            \/ \E job \in Jobs : ReclaimExpired(job)
            \/ IssueSession
            \/ \E session \in 0..MaxRestarts : Logout(session)
            \/ Stop
            \/ JoinWorkers
            \/ Close
            \/ StopBrowser
            \/ Restart)
        /\ Remember

Spec == /\ Init
        /\ [][Next]_vars
        /\ WF_vars(Start /\ Remember)
        /\ WF_vars(Bootstrap /\ Remember)
        /\ WF_vars(JoinWorkers /\ Remember)
        /\ WF_vars(Close /\ Remember)

TypeOK == /\ mode \in {"browser", "postgres", "memory"}
          /\ topology \in Topologies
          /\ phase \in {"cold", "browser", "rejected", "recovering",
                         "running", "stopping", "closed"}
          /\ epoch \in 0..MaxRestarts
          /\ opened \in BOOLEAN
          /\ bootstrapReady \in BOOLEAN
          /\ markers \subseteq Jobs
          /\ ready \subseteq Jobs
          /\ leases \in [Jobs -> {-1} \cup (0..MaxRestarts)]
          /\ completed \subseteq Jobs
          /\ active \subseteq Jobs
          /\ sessions \subseteq 0..MaxRestarts
          /\ checkpointPages \subseteq Jobs
          /\ evidence \subseteq Jobs
          /\ admissionAuthorized \in BOOLEAN
          /\ history.phase \in {"cold", "browser", "rejected", "recovering",
                                 "running", "stopping", "closed"}
          /\ history.evidence \subseteq Jobs
          /\ history.protected \in
              ((SUBSET Jobs) \X (SUBSET Jobs) \X [Jobs -> {-1} \cup (0..MaxRestarts)]
               \X (SUBSET Jobs) \X (SUBSET (0..MaxRestarts)) \X (SUBSET Jobs))

PolicyAuthority == opened => SelectionAllowed
BrowserIsolation == mode = "browser" => ~opened /\ markers = {} /\ sessions = {}
BootstrapGate == bootstrapReady => phase = "running" /\ opened
AdmissionGate == admissionAuthorized
CapacityBound == Cardinality(Pending) <= Capacity
AtomicAdmission == /\ markers = ready \cup Leased \cup completed
                   /\ ready \cap Leased = {}
                   /\ ready \cap completed = {}
                   /\ Leased \cap completed = {}
BackfillOnlyAfterAdmission == checkpointPages \subseteq markers
OwnerCheckedWork == /\ active \subseteq Leased
                    /\ \A job \in active : leases[job] = epoch
CloseAfterJoin == ~opened => active = {}
MemorySessionEpoch == Backend = "memory" => sessions \subseteq {epoch}
MemoryLossIsExplicit == (Backend = "memory" /\ phase \in {"cold", "closed", "rejected"})
                       => markers = {} /\ sessions = {} /\ checkpointPages = {}

EvidencePersists == history.evidence \subseteq evidence
DurableRestartPersistence ==
    (Backend = "postgres" /\
     ((history.phase = "stopping" /\ phase = "closed") \/
      (history.phase = "closed" /\ phase = "cold")))
        => history.protected = OperationalState

BootstrapCompletes == phase = "recovering" ~> phase \in {"running", "stopping", "closed"}
ShutdownCompletes == phase = "stopping" ~> phase = "closed"
=============================================================================
