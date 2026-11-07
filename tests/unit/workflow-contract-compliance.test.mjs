import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { root, workflow } from "./workflow-contract.helpers.mjs";

// Advisory compliance operation boundaries (UK AI, EU CRA, Dev Practices).

test("Article 14 terminates duplicate readiness work without undeclared update tools", () => {
  const source = workflow("eu-cra-compliance-article-14-reporting-readiness.md");
  const compiled = workflow("eu-cra-compliance-article-14-reporting-readiness.lock.yml");
  const control = workflow("shared/control.md");

  assert.match(control, /When an open issue already represents the same underlying work,[^\n]*otherwise call `noop`/);
  assert.match(source, /exactly one terminal safe-output call: `create_issue`[^\n]*or `noop`/);
  assert.match(source, /open campaign-and-worker-owned issue in `SAFE_OUTPUT_REPO`[^\n]*same target's readiness work/);
  assert.match(source, /even when the target SHA, evidence, or authoritative requirements have changed/);
  assert.match(source, /`noop` reason,[^\n]*link the existing issue[^\n]*`INCOMPLETE`/);
  assert.match(source, /do not claim that the existing record is current or that this audit was published/);
  assert.match(source, /Do not create a duplicate or call `report_missing_tool` merely because updating the existing record is not configured/);
  assert.doesNotMatch(source, /Use `noop` only for an equivalent current readiness record/);
  assert.doesNotMatch(source, /^\s+(update-issue|add-comment):/m);
  assert.match(compiled, /"tools":\["create_issue","noop","report_incomplete","report_missing_data","report_missing_tool"\]/);
});

test("Advisory preserves UK AI guidance and human-review boundaries", () => {
  const orchestrator = workflow("uk-ai-advisory.md");
  const maintainer = workflow("uk-ai-advisory-campaign-maintainer.md");
  const compiledMaintainer = workflow("uk-ai-advisory-campaign-maintainer.lock.yml");
  const worker = workflow("uk-ai-advisory-operational-resilience.md");
  const readme = readFileSync(join(root, "uk-ai-advisory", "README.md"), "utf8");

  assert.match(orchestrator, /^name: "UK AI Advisory"$/m);
  assert.match(worker, /^name: "UK AI Advisory \/ Resilience"$/m);
  for (const source of [orchestrator, worker, readme]) {
    assert.match(source, /advisory and non-binding/i);
    assert.match(source, /no guarantee of completeness, correctness, accuracy/i);
    assert.match(source, /human review/i);
  }

  assert.match(orchestrator, /schedule: "hourly"/);
  assert.match(orchestrator, /workflows: \[uk-ai-advisory-operational-resilience\]/);
  assert.match(orchestrator, /Use bounded two-stage discovery/);
  assert.match(orchestrator, /AI is a threat accelerator, not an eligibility requirement/);
  assert.match(orchestrator, /prolonged inactivity without credible ownership or automated hygiene is a priority signal/);
  assert.match(worker, /https:\/\/www\.gov\.uk\/guidance\/ai-open-code-and-vulnerability-risk-in-the-public-sector/);
  assert.match(worker, /incomplete by design/i);
  assert.match(worker, /do not authorize opening, restricting, hiding, or decommissioning code/i);
  assert.match(worker, /If the guidance, repository metadata, commits, or another required source is inaccessible, stop analysis, call `report_incomplete`/);
  assert.match(worker, /source_access/);
  assert.match(worker, /repository_metadata/);
  assert.match(worker, /visibility: repositoryData\.visibility/);
  assert.match(worker, /open_dependabot_alerts/);
  assert.match(worker, /secret-scanning-alerts: read/);
  assert.match(worker, /job-discriminator: \$\{\{ github\.run_id \}\}/);
  assert.match(worker, /dependency_automation/);
  assert.match(worker, /security_policy/);
  assert.match(worker, /age_days: ageDays\(alert\.created_at\)/);
  assert.match(worker, /secret_type_display_name/);
  assert.doesNotMatch(worker, /alert\.secret\b/);
  assert.match(worker, /Open by default/);
  assert.match(worker, /patch SLAs and remediation capability/);
  assert.match(worker, /rapid response to inbound vulnerability reports/);
  assert.match(worker, /credible attacker, what publication adds to the risk, the realistic path to harm/);
  assert.match(worker, /named re-approval owner and cadence/);
  assert.match(worker, /cap the proposed tier at B/);
  assert.match(worker, /A public repository with no recent commits and no evidence of active ownership or automated hygiene requires a dormancy finding/);
  assert.match(worker, /patch_sla_controls/);
  assert.match(worker, /disclosure_controls/);
  assert.match(worker, /max: 1/);
  assert.match(worker, /close-older-issues: true/);
  assert.match(worker, /## agent: `asset-tier-classifier`/);
  assert.match(worker, /## agent: `control-verifier`/);
  assert.match(worker, /## agent: `ai-risk-scorer`/);
  assert.doesNotMatch(worker, /^graders:/m);

  assert.match(maintainer, /^name: "UK AI Advisory \/ Maintenance"$/m);
  assert.match(maintainer, /schedule: weekly/);
  assert.match(maintainer, /safe_output_mode:\n\s+default: review/);
  assert.doesNotMatch(maintainer, /^\s+staged:/m);
  assert.match(maintainer, /original specification and current authoritative GOV\.UK guidance/);
  assert.match(maintainer, /https:\/\/www\.gov\.uk\/guidance\/ai-open-code-and-vulnerability-risk-in-the-public-sector/);
  assert.match(maintainer, /update only the applicable ledger path/i);
  assert.match(maintainer, /allowed-files:\n\s+- "uk-ai-advisory\/implementation-status\.md"/);
  assert.doesNotMatch(maintainer, /\.github\/aw\/uk-ai-advisory/);
  assert.doesNotMatch(compiledMaintainer, /\.github\/aw\/uk-ai-advisory/);
  assert.match(maintainer, /draft: true/);
  assert.match(maintainer, /create-issue:[\s\S]*?deduplicate-by-title: true[\s\S]*?max: 1/);
  assert.match(maintainer, /If the authoritative source or a trusted campaign file cannot be accessed or reconciled, call `report_incomplete`/);
  assert.match(maintainer, /Emit `noop` only after the authoritative source and every trusted file were evaluated successfully/);
  assert.doesNotMatch(maintainer, /shared\/control\.md/);
  assert.doesNotMatch(maintainer, /^graders:/m);

  const ledger = readFileSync(join(root, "uk-ai-advisory", "implementation-status.md"), "utf8");
  assert.match(ledger, /UK-AI-001/);
  assert.match(ledger, /UK-AI-015/);
  assert.match(ledger, /AI is a threat accelerator, not an eligibility requirement/);
  assert.match(ledger, /credible attacker, what publication adds to risk, and the realistic path to harm/);
  assert.match(ledger, /It does not prove that the campaign, an installed fleet, a repository, or an organization is secure/);
});

test("UK AI advisory worker uses actionable progressive-disclosure reports", () => {
  const worker = workflow("uk-ai-advisory-operational-resilience.md");

  assert.match(worker, /executive summary[\s\S]*decision-relevant result[\s\S]*key metrics[\s\S]*recommended next action/i);
  assert.match(worker, /Keep critical findings[\s\S]*recommended next action visible/i);
  assert.match(worker, /non-essential background[\s\S]*verbose supporting evidence[\s\S]*per-item breakdowns[\s\S]*`<details>`/i);
  assert.match(worker, /single most important action with the highest expected return on investment/i);
  assert.match(worker, /<details><summary><b>Agent prompt<\/b><\/summary>/);
  assert.match(worker, /clear, imperative prompt for an agentic run that performs only that selected action/i);
});

test("EU CRA workflows preserve advisory and human-review boundaries", () => {
  const orchestrator = workflow("eu-cra-compliance.md");
  const maintainer = workflow("eu-cra-compliance-campaign-maintainer.md");
  const compiledMaintainer = workflow("eu-cra-compliance-campaign-maintainer.lock.yml");
  const workers = [
    ["eu-cra-compliance-scope-classifier.md", "Scope"],
    ["eu-cra-compliance-security-requirements-auditor.md", "Security"],
    ["eu-cra-compliance-supply-chain-sbom-auditor.md", "Supply Chain"],
    ["eu-cra-compliance-vulnerability-handling-auditor.md", "Vulnerabilities"],
    ["eu-cra-compliance-article-14-reporting-readiness.md", "Article 14"],
    ["eu-cra-compliance-conformity-release-evidence.md", "Conformity"],
  ];

  assert.match(orchestrator, /^name: "EU CRA"$/m);
  assert.match(orchestrator, /advisory and non-binding/i);
  assert.match(orchestrator, /no guarantee of completeness, correctness, accuracy, or alignment with the EU Cyber Resilience Act/i);
  assert.match(orchestrator, /must not analyze a target repository for CRA compliance/i);
  assert.match(orchestrator, /Use bounded two-stage discovery/);
  assert.match(orchestrator, /plus at most two alternates per available slot/);
  assert.match(orchestrator, /sum of enabled, useful workers across selected repositories/);
  assert.match(orchestrator, /Keep that total at or below 48/);
  assert.match(orchestrator, /worker_credits_per_target: 600/);

  for (const [name, displayName] of [["eu-cra-compliance.md", null], ...workers, ["eu-cra-compliance-campaign-maintainer.md", "Maintenance"]]) {
    const source = workflow(name);
    if (displayName) {
      assert.match(source, new RegExp(`^name: "EU CRA / ${displayName}"$`, "m"));
    }
    assert.match(source, /engine:\n\s+id: pi\n\s+model: copilot\/gpt-5\.6-sol/);
    assert.match(source, /copilot-requests: write/);
    assert.match(source, /tools:\n\s+cli-proxy: true\n\s+github:\n\s+mode: gh-proxy/);
  }

  for (const [name, displayName] of workers) {
    const source = workflow(name);
    assert.match(source, /Regulation \(EU\) 2024\/2847/);
    assert.match(source, /https:\/\/eur-lex\.europa\.eu\/eli\/reg\/2024\/2847\/oj/);
    assert.match(source, /https:\/\/digital-strategy\.ec\.europa\.eu\/en\/policies\/cyber-resilience-act/);
    assert.match(source, /source:\n\s+instrument: "Regulation \(EU\) 2024\/2847"\n\s+provision: ".+"\n\s+authority: "binding"/);
    assert.match(source, /HUMAN_REVIEW_REQUIRED/);
    assert.match(source, /commercial versus non-commercial FOSS treatment/);
    assert.match(source, /important Class I or Class II classification/);
    assert.match(source, /active exploitation, the severe-incident threshold, reportability/);
    assert.match(source, /Never output `CRA COMPLIANT`, `LEGALLY COMPLIANT`, `CERTIFIED`, or `CE APPROVED`/);
    assert.match(source, /Never (?:submit|notify)/i);
    assert.match(source, /Do not put secrets, personal data, exploit details/);
    assert.match(source, /<!-- operational-value: domain=[a-z0-9-]+ target=OWNER\/REPO target-sha=40_HEX_SHA -->/);
    assert.match(source, /### Human Acceptance/);
    assert.match(source, /max-ai-credits: 100/);
    assert.match(source, /exact unprefixed title `TARGET_REPO CRA/);
    assert.match(source, /Write concise technical English/);
    assert.match(source, /small moment of delight/);
    assert.match(source, /shared progressive-disclosure contract/);
    assert.match(source, /at most 18 evidence-gathering tool calls/);
    assert.match(source, /Do not repeat an equivalent search or fetch with another tool/);
    assert.match(source, /keep the issue body at or below 1,500 words/);
  }

  assert.match(maintainer, /schedule: daily/);
  assert.match(maintainer, /safe_output_mode:\n\s+default: review/);
  assert.doesNotMatch(maintainer, /^\s+staged:/m);
  assert.match(maintainer, /Systematically account for the complete Act: Articles 1–71, Annexes I–VIII/);
  assert.match(maintainer, /update only the applicable ledger path/i);
  assert.match(maintainer, /allowed-files:\n\s+- "eu-cra-compliance\/implementation-status\.md"/);
  assert.doesNotMatch(maintainer, /\.github\/aw\/eu-cra-compliance/);
  assert.doesNotMatch(compiledMaintainer, /\.github\/aw\/eu-cra-compliance/);
  assert.match(maintainer, /draft: true/);
  assert.match(maintainer, /create-issue:[\s\S]*?max: 1/);
  assert.match(maintainer, /deduplicate-by-title: true/);
  assert.doesNotMatch(maintainer, /shared\/control\.md/);

  const ledger = readFileSync(join(root, "eu-cra-compliance", "implementation-status.md"), "utf8");
  assert.match(ledger, /Articles 1–12/);
  assert.match(ledger, /CRA-ART-001/);
  assert.match(ledger, /Articles 60–71/);
  assert.match(ledger, /Annexes II–VIII/);
  assert.match(ledger, /CRA-ACTS-001/);
  assert.match(ledger, /`IMPLEMENTED` means a workflow capability exists/);

  const article14 = workflow("eu-cra-compliance-article-14-reporting-readiness.md");
  assert.match(article14, /without undue delay and, in any event, no later than 24 hours/);
  assert.match(article14, /without undue delay and, in any event, no later than 72 hours/);
  assert.match(article14, /no later than 14 days after a corrective or mitigating measure becomes available/);
  assert.match(article14, /no later than one month after submission of the incident notification/);
  assert.match(article14, /vulnerability description, severity and impact, available malicious-actor information/);
  assert.match(article14, /detailed incident description, severity and impact, likely threat type or root cause/);
  assert.match(article14, /never expose sensitive details in the issue/);
  assert.match(article14, /intermediate status report when requested by the CSIRT coordinator/);
  assert.match(article14, /awareness of either an actively exploited vulnerability or a severe incident having an impact on product security/);
  assert.match(article14, /affected users and, where appropriate, all users without undue delay/);
  assert.match(article14, /Do not incorrectly make user communication contingent on completion of a regulatory notification/);
  assert.match(article14, /Do not start or calculate an SLA clock from a guessed timestamp/);
  assert.match(article14, /manufacturer-awareness evidence cannot be determined, report a critical evidence gap/);

  const security = workflow("eu-cra-compliance-security-requirements-auditor.md");
  assert.match(security, /absence of known exploitable vulnerabilities at market placement/);
  assert.doesNotMatch(security, /absence or reduction of known exploitable vulnerabilities/);
  assert.match(security, /leave operational distribution and remediation-process evidence to the vulnerability-handling auditor/);
  assert.match(security, /Every `GAP_FOUND`, `HUMAN_REVIEW_REQUIRED`, `NOT_ASSESSED`, and `INCOMPLETE` row MUST have an accountable owner and disposition/);
  assert.match(security, /Use `assigned` only with a verifiable issue or pull request and assignee/);
  assert.match(security, /otherwise mark it `pending assignment`/);
  assert.match(security, /confirming that every `INCOMPLETE`, `NOT_ASSESSED`, and `HUMAN_REVIEW_REQUIRED` row has an owner and disposition/);
  assert.match(security, /If any such row lacks either, state that acceptance is blocked/);

  const supplyChain = workflow("eu-cra-compliance-supply-chain-sbom-auditor.md");
  assert.match(supplyChain, /machine-readable SBOM covering at least top-level dependencies/);
  assert.match(supplyChain, /Annex I, Part II, point \(1\)/);
  assert.match(supplyChain, /implementation evidence beyond that express minimum/);

  const conformity = workflow("eu-cra-compliance-conformity-release-evidence.md");
  assert.match(conformity, /at least 10 years after market placement or for the support period, whichever is longer/);

  assert.match(ledger, /CRA-ART-014.*reportability requires human review \| (?:IMPLEMENTED|PARTIAL) \|/);
  assert.match(ledger, /CRA-ART-028-031.*final release require human review \| (?:IMPLEMENTED|PARTIAL) \|/);
  assert.match(ledger, /CRA-ANNEX-VIII.*Route selection requires human review \| (?:IMPLEMENTED|PARTIAL) \|/);
});

test("Dev Practices preserves evidence and advisory boundaries", () => {
  const orchestrator = workflow("software-development-practices.md");
  const githubWorker = workflow("software-development-practices-github-well-architected.md");
  const nistWorker = workflow("software-development-practices-nist-ssdf.md");
  const readme = readFileSync(join(root, "software-development-practices", "README.md"), "utf8");

  assert.match(orchestrator, /^name: "Dev Practices"$/m);
  assert.match(githubWorker, /^name: "Dev Practices \/ Well-Architected"$/m);
  assert.match(nistWorker, /^name: "Dev Practices \/ NIST SSDF"$/m);
  assert.match(orchestrator, /workflows:\n\s+- software-development-practices-github-well-architected\n\s+- software-development-practices-nist-ssdf/);
  assert.match(orchestrator, /Use bounded two-stage discovery/);
  assert.match(orchestrator, /Keep the total at or below 20/);
  assert.match(orchestrator, /job-discriminator: \$\{\{ github\.run_id \}\}/);
  assert.match(orchestrator, /toolsets: \[repos, issues, pull_requests, actions\]/);
  assert.doesNotMatch(orchestrator, /security-events: read|vulnerability-alerts: read|web-fetch:/);
  assert.doesNotMatch(orchestrator, /^\s+create-issue:/m);

  for (const source of [orchestrator, githubWorker, nistWorker, readme]) {
    assert.match(source, /advisory and non-binding/i);
    assert.match(source, /human review/i);
  }
  for (const source of [orchestrator, readme]) {
    assert.match(source, /no guarantee of completeness, correctness/i);
  }

  for (const worker of [githubWorker, nistWorker]) {
    assert.match(worker, /OBSERVED.*PARTIAL.*GAP_FOUND.*HUMAN_REVIEW_REQUIRED.*NOT_ASSESSED.*INCOMPLETE/s);
    assert.match(worker, /analyzed commit SHA/);
    assert.match(worker, /create-issue:[\s\S]*?close-older-issues: true[\s\S]*?close-older-key:.*inputs\.target_repo[\s\S]*?max: 1/);
    assert.match(worker, /^\s+web-fetch:$/m);
    assert.match(worker, /<!-- operational-value: framework=[a-z0-9-]+ target=OWNER\/REPO target-sha=40_HEX_SHA -->/);
  }
  assert.match(readme, /Operational value is attainment-only/);

  assert.match(githubWorker, /https:\/\/learn\.github\.com\/well-architected\//);
  assert.match(githubWorker, /^\s+- wellarchitected\.github\.com$/m);
  assert.match(githubWorker, /github\/github-well-architected/);
  assert.match(githubWorker, /GitHub Docs, which the framework identifies as the implementation source of truth/);
  assert.match(githubWorker, /Leave secure-development lifecycle practices.*to the NIST SSDF worker/);
  for (const pillar of ["Productivity", "Collaboration", "Application Security", "Governance", "Architecture"]) {
    assert.match(githubWorker, new RegExp(pillar));
  }
  assert.match(githubWorker, /does not prove security, compliance, certification, endorsement, or complete alignment/);

  assert.match(nistWorker, /https:\/\/csrc\.nist\.gov\/projects\/ssdf/);
  assert.match(nistWorker, /https:\/\/csrc\.nist\.gov\/pubs\/sp\/800\/218\/final/);
  assert.match(nistWorker, /https:\/\/doi\.org\/10\.6028\/NIST\.SP\.800-218/);
  assert.match(nistWorker, /Build a complete practice-level matrix/);
  assert.match(nistWorker, /Leave developer experience.*to the GitHub Well-Architected worker/);
  for (const group of ["Prepare the Organization", "Protect the Software", "Produce Well-Secured Software", "Respond to Vulnerabilities"]) {
    assert.match(nistWorker, new RegExp(group));
  }
  assert.match(nistWorker, /Identify drafts separately as non-final and do not score the repository against draft requirements/);
  assert.match(nistWorker, /does not prove security, compliance, certification, endorsement, or SSDF conformance/);
});
