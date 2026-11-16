# Debug Central Agentic Ops

## Deterministic boundary

Use `gh run view`, `gh aw logs`, the `cao-validation` Actions report artifact, and CAO
query or diagnostic commands to collect structured evidence. Do not reconstruct
run metadata, package identity, policy validity, or query results manually. The
agent owns boundary classification, falsifiable hypotheses, evidence
interpretation, and the next bounded observation.

## Procedure

1. Start read-only. Preserve the failed attempt before rerunning, updating,
   recompiling, rotating credentials, changing policy, or enabling live mode.
2. Record the control repository, run and attempt, event, ref, head SHA,
   `GITHUB_WORKFLOW_SHA`, failing job and step, UTC times, runner image, target,
   effective campaign/worker/mode, and review destination.
3. Record immutable component identities: campaign `resolvedCommit`,
   `.github/workflows/cao.json` version and `gh-aw-version`, `gh aw`, GitHub CLI,
   Node, actions, runtime, and target revisions.
4. Read the run summary and annotations, then the smallest log excerpt containing
   the first causal error and its preceding context.
5. Use the [failure routes](diagnostics/failure-routes.md) to inspect the narrowest
   boundary at the failing workflow commit.
6. Classify exactly one primary boundary: installation/version drift,
   policy/admission, authentication/authorization, Copilot inference, gh-aw
   compile/runtime, campaign logic, Activity data, dashboard build/deploy, or
   GitHub Actions/platform.
7. State evidence excluding adjacent categories. Form one falsifiable hypothesis:
   at `<step>`, `<component@revision>` fails because `<condition>`;
   `<observation>` would confirm or reject it.
8. Reproduce only when it stays bounded and review-safe, holding workflow,
   policy, compiler, target, and inputs constant and changing one boundary.
   For a local worker loop, follow [Local Worker Debugging](https://github.com/githubnext/gh-cao/blob/main/docs/local-debugging.md):
   use `gh aw run WORKER --repo CONTROL --ref REF` with explicit
   `target_repo`, matching `safe_output_repo`, and `safe_output_mode=debug`
   through `--raw-field`. Omit dispatcher correlation inputs. Use the human
   operator's authentication with control-repository write access, then inspect
   the run using `gh run`. Never launch debug from an orchestrator or bot,
   change policy to `debug`, or claim that staged proposals were executed.
   Repository-memory staging depends on gh-aw support.
9. Search for an exact existing issue before creating one. Use the
   [diagnostic report](diagnostics/diagnostic-report.md); include immutable links,
   redactions, the next bounded observation, and an acceptance condition.
10. Report incomplete diagnosis rather than guessing when required evidence is
    inaccessible.

## Guardrails

- Keep CAO authority separate from gh-aw execution capability. Credentials,
  compiled permissions, and successful API calls do not widen policy.
- Never print, copy, attach, or request secret values, private keys,
  authorization headers, signed URLs, raw prompts, or private target data.
- If the report concerns a vulnerability or exposed credential, stop public
  issue creation and use the repository security policy.
- Inspect `.md` workflow sources at the failing revision. Never edit generated
  `.lock.yml` files.
- Do not infer that a dashboard symptom originated in the dashboard; prove the
  Activity snapshot and handoff first.

## Targeted references

- [Failure routes](diagnostics/failure-routes.md): symptom-to-boundary dispatch,
  source locations, and diagnostics.
- [Diagnostic report](diagnostics/diagnostic-report.md): issue fields and
  completion checklist.
- [Monitor, Recover, and Maintain](https://github.com/githubnext/gh-cao/blob/main/docs/operations.md): operational
  recovery.
- [Authentication](https://github.com/githubnext/gh-cao/blob/main/docs/authentication.md): credential profiles.
- [Execution and Safety](https://github.com/githubnext/gh-cao/blob/main/docs/execution-and-safety.md): authority and
  worker boundaries.
