# Public control repositories

Load this reference before selecting a public control repository or when any
target is non-public.

Public control repositories expose their checked-in policy, workflow runs,
operational metadata, dashboard data, and review safe outputs. Preserve an
existing repository's visibility; for a new repository, use the visibility the
user explicitly chooses after this exposure is stated.

Recommend private unless every target and every item of operational evidence
may be public. A public control repository with a private or internal target can
disclose non-public evidence even when credentials remain secret. Fail closed
and require a private control repository or entirely public scope before setup
continues.

Never place confidential target names, evidence, prompts, payloads, credentials,
or review output in a public repository. Visibility does not grant target
consent or expand `.github/workflows/cao.json`.

Non-interactive setup supports only an existing public control repository with
empty campaigns and absent or exact self-only scope. It selects repository
`GITHUB_TOKEN`, configures public Actions-backed Pages, and keeps read and
Activity scope at that repository. Any broader scope requires interactive
review.
