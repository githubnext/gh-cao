----------------------- MODULE OAuthAccountSelection -----------------------
\* Bounded model of dashboard GitHub sign-in when one browser holds several
\* GitHub identities. GitHub chooses the callback identity nondeterministically:
\* prompt=select_account and the login hint are advisory, never trusted.
EXTENDS Naturals, Sequences, FiniteSets
CONSTANTS Accounts, Allowed, MaxHints, None
ASSUME Allowed \subseteq Accounts /\ None \notin Accounts /\ MaxHints >= 2

Pages == {"idle", "github", "dashboard", "failure", "select", "loggedOut"}
Hint == [id : Accounts, authorized : BOOLEAN]

VARIABLES page, session, pendingHint, pendingConsent, hints, seen, lastAccount
vars == <<page, session, pendingHint, pendingConsent, hints, seen, lastAccount>>

HintIds(hs) == {hs[i].id : i \in DOMAIN hs}
Min(a, b) == IF a < b THEN a ELSE b
\* rememberAccount: newest first, deduplicated by ID, bounded.
Remember(hs, a, ok) ==
    LET merged == <<[id |-> a, authorized |-> ok]>> \o SelectSeq(hs, LAMBDA h : h.id # a)
    IN SubSeq(merged, 1, Min(Len(merged), MaxHints))
HasOther(hs, a) == \E i \in DOMAIN hs : hs[i].id # a

Init ==
    /\ page = "idle" /\ session = None
    /\ pendingHint = None /\ pendingConsent = FALSE
    /\ hints = <<>> /\ seen = {} /\ lastAccount = None

\* /auth/login, optionally with a syntactically valid login hint. Consent
\* (re-authorization) flows start only from an existing session.
Login(h, consent) ==
    /\ page # "github"
    /\ consent => session # None
    /\ page' = "github" /\ pendingHint' = h /\ pendingConsent' = consent
    /\ UNCHANGED <<session, hints, seen, lastAccount>>

\* /auth/callback with the identity GitHub selected. Denied credentials are
\* revoked, so only an allowed identity ever becomes the session.
Callback(a) ==
    /\ page = "github"
    /\ lastAccount' = a /\ seen' = seen \cup {a}
    /\ pendingHint' = None /\ pendingConsent' = FALSE
    /\ IF a \in Allowed
       THEN /\ session' = a /\ page' = "dashboard"
            /\ hints' = Remember(hints, a, TRUE)
       ELSE /\ session' = session
            /\ IF pendingConsent
               THEN /\ hints' = hints /\ page' = "failure"
               ELSE /\ hints' = Remember(hints, a, FALSE)
                    /\ page' = IF HasOther(hints', a) THEN "select" ELSE "failure"

\* Direct /auth/select-account visit: fewer than two hints redirects to a
\* plain account-selection login.
VisitSelect ==
    /\ page # "github"
    /\ IF Len(hints) >= 2
       THEN page' = "select" /\ UNCHANGED <<pendingHint, pendingConsent>>
       ELSE page' = "github" /\ pendingHint' = None /\ pendingConsent' = FALSE
    /\ UNCHANGED <<session, hints, seen, lastAccount>>

\* Choosing an account on the selection page is Login with that hint.
Choose(a) ==
    /\ page = "select" /\ a \in HintIds(hints)
    /\ Login(a, FALSE)

Logout ==
    /\ page # "github"
    /\ page' = "loggedOut" /\ session' = None /\ hints' = <<>> /\ seen' = {}
    /\ UNCHANGED <<pendingHint, pendingConsent, lastAccount>>

Next ==
    \/ \E h \in Accounts \cup {None}, c \in BOOLEAN : Login(h, c)
    \/ \E a \in Accounts : Callback(a) \/ Choose(a)
    \/ VisitSelect
    \/ Logout
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ page \in Pages /\ session \in Accounts \cup {None}
    /\ pendingHint \in Accounts \cup {None} /\ pendingConsent \in BOOLEAN
    /\ Len(hints) <= MaxHints /\ \A i \in DOMAIN hints : hints[i] \in Hint
    /\ seen \subseteq Accounts /\ lastAccount \in Accounts \cup {None}

SessionAuthorized == session # None => session \in Allowed
HintsUnique == \A i, j \in DOMAIN hints : i # j => hints[i].id # hints[j].id
HintsFromCallbacks == HintIds(hints) \subseteq seen
AuthorizedHintsAccurate == \A i \in DOMAIN hints : hints[i].authorized => hints[i].id \in Allowed
SelectionNeedsAlternatives == page = "select" => Len(hints) >= 2

\* Hints are advisory: a session changes only through an allowed callback or
\* logout, never from the hint cookie or selection page.
HintsNeverGrantAuthority ==
    [][session' # session =>
        \/ session' = None /\ page' = "loggedOut"
        \/ page = "github" /\ session' = lastAccount' /\ session' \in Allowed]_vars
\* Only an ordinary denied callback with another known identity redirects.
SelectionOnlyAfterOrdinaryDenial ==
    [][page = "github" /\ page' = "select" =>
        /\ ~pendingConsent
        /\ lastAccount' \notin Allowed
        /\ HasOther(hints', lastAccount')]_vars
\* Consent flows keep the existing failure page and do not record hints.
ConsentFailureUnchanged ==
    [][page = "github" /\ pendingConsent /\ page' # "dashboard" =>
        page' = "failure" /\ hints' = hints]_vars
LogoutForgetsIdentities ==
    [][page' = "loggedOut" /\ page # "loggedOut" => hints' = <<>> /\ session' = None]_vars
=============================================================================
