---
title: Private-preview acceptance
description: Bound the evaluation promise and demonstrate a candidate-specific operator journey before inviting a cohort.
---

**Status: proposed, not launch-approved.** The
[preview contract](https://github.com/githubnext/gh-cao/blob/main/.github/cao/private-preview.json)
proposes monitoring-first evaluation on GitHub.com, one explicitly enrolled
repository, Linux/macOS operators and access-restricted Pages. This proposal
does not select a release, invite participants, approve data use, change CAO
policy or enable execution. Null fields are unresolved decisions, not defaults.

The goal is a small experience an invited team can safely install, use, operate
and leave, not universal architectural completion. Other topologies require
their own reviewed acceptance boundary; optional providers and campaigns must
not become implicit prerequisites. Private evidence must never be published
until actual dashboard access has been verified. A private control repository
alone does not establish that boundary.

## Agree the preview promise

Before inviting a cohort, maintainers must review the contract in a normal PR:
name the decision/support owners and journey owners; choose a published
candidate `{ "tag": "vX.Y.Z", "sha": "<full commit SHA>" }`; define the support
channel and response expectations, cohort limit, permitted evaluation data,
`recovery_objective` and a `retention_reference` for the agreed per-copy
retention/deletion rules. Record the
human scope approval reference and change `status` to `approved` only after
that review. Scope approval is not launch approval or runtime authority.

Keep the journey IDs and acceptance wording stable. A changed candidate,
profile, platform set or acceptance check invalidates the corresponding prior
acceptance; the report records the full contract's SHA-256 digest so it cannot
silently compare different promises. Do not copy CAO rollout policy into this
record. Enrollment, credentials and execution remain separately authorized.

## Prove the supported journey

| Journey | Required evidence beyond source/test presence |
| --- | --- |
| `install` | Exact published candidate and verified assets; clean installation on every declared platform. |
| `connect` | Independent operator walkthrough with actual grants and expected denials; no execution enrollment. |
| `collect` | Scoped candidate collection, verified snapshot and honest duplicate/partial/failure behavior. |
| `inspect-refresh` | Populated views, source drill-down and refresh through production worker queries on desktop/mobile. |
| `access-withdraw` | Allowed and denied viewers at the actual deployment; enrollment/credential withdrawal exercised. |
| `update-recover` | Policy-preserving update, stop/diagnosis and exercised recovery against the agreed objective. |
| `offboard` | Future collection stopped and each retained/published copy disposed of according to the agreed rules. |
| `participant-value` | Representative tasks, completion denominator, assistance/time, correctness/usefulness and support burden. |

Use the existing installer/deployed canaries, runtime packaging, integration
tests and [release controls](release-controls.md); do not build a parallel
promotion or governance service. CI metadata is supporting evidence, not proof
that a participant installed that release or that a live permission boundary
works. Simulator coverage remains distinct from
[live onboarding verification](setup-compatibility.md#live-verification-still-required).
Release-promotion workflow `head_sha` identifies its trusted workflow source,
not necessarily the release candidate it validated. A deployed-dashboard
check can use different deployed data or source: its run SHA alone does not
bind a deployment to the candidate.

Human reviewers retain applicable exact-candidate evidence and acceptance in
the approved evidence system. Public reports link only sanitized, accessible
summaries; never publish participant identities, private repository names,
credentials or raw customer evidence. Applicable findings in `server/SOC2.md`
remain in that register, not a second daily finding database. SOC 2
certification is not a blanket prerequisite for this bounded evaluation.

## Decide, then expand cautiously

Invite a small cohort only after required journeys and essential access,
scope and data boundaries have evidence, owners have accepted remaining
limitations, and support/stop/recovery procedures are usable. A required
unknown is not a pass and cannot be averaged away by healthy components.
Deliberately excluded capabilities are not failed preview journeys.

Expand after reviewing participant task outcomes and operational burden, not
after a fixed number of quiet days or a churn reduction. Unexpected access,
disclosure, mutation or failed required journeys stop expansion and trigger
the agreed response. No automated report admits a cohort, promotes a release,
changes policy, or accepts risk on behalf of maintainers.

## Daily evidence, not a launch certificate

The [daily framework report](operations.md#daily-cao-framework-health) leads
with the candidate/profile, journey blockers, accountable owners, closure
checks and participant-evidence gaps. Its eight-component/16-claim core-health
register and churn charts are supporting detail, not a readiness score.

Daily work checks changed evidence against a stable baseline, rather than
trying to re-audit the entire framework. Missing or changed boundaries stay
unknown. The deterministic `framework-health-evidence-RUN-ATTEMPT` Actions
artifact retains the contract/digest, candidate release metadata, bounded
workflow/job observations, CI windows and component churn for 90 days. It does
not contain agent-generated acceptance decisions or customer evidence, and
must be retained elsewhere if a longer evidence period is required.

The fixed executive format and collector tests do not establish that every
model-written conclusion is correct. Human review must confirm citations,
journey applicability, candidate binding and claimed closure before acting.
