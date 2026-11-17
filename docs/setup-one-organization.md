---
title: Set Up One Organization
description: Install a CAO control plane for private repositories in one organization using private GitHub Apps.
---

You need an approved private App pair and authorized access to its keys, or
permission to create and install private Apps for the organization. Repository
administrators can extend an approved installation to their repositories where
organization policy permits; new installations still need appropriate approval.

> [!IMPORTANT]
> Permission does not authorize setup. A coding agent must ask before creating or reusing the control repository, installing the extension/runtime, creating either App, storing variables or secrets, committing, or pushing.

## Install

Complete [Step 1 of Set Up CAO](setup-quickstarts.md#step-1---install-cao):
install the extension and enter a clean default-branch control checkout.
That guide also covers creating versus cloning the repository and the
[SAML installer fallback](cao-cli.md#control-plane-installer).
From the control checkout:

```bash
CONTROL_REPO="acme/central-agentic-ops"
TARGET_REPO="acme/example-service"

gh cao setup
```

At **Repositories**, enter the exact value of `$TARGET_REPO`; the control
repository stays included. Choose **`github-app`** when authentication is
prompted. Setup prepares Activity, Dashboard, validation, shared runtime and
missing baseline policy, then configures scoped authentication and Pages.
A private repository does not guarantee private Pages: require restricted
access for non-public evidence and decline public publication otherwise.

## Configure the Apps

If Apps are already approved, prefer
[reusing their client IDs](authentication.md#reuse-approved-apps)
with `--read-client-id` and `--write-client-id`, or choose **`approved-app`**
in guided setup. Keys are entered only at hidden prompts.
Otherwise create a pair through the guided prompts.
For credential-only changes after setup, inspect a dry run before applying:

```bash
gh cao setup auth github-app \
  --repo "$CONTROL_REPO" \
  --dry-run

gh cao setup auth github-app \
  --repo "$CONTROL_REPO"
```

Follow the [App provisioning procedure](control-plane-authentication.md#configure-private-github-apps)
for both browser flows, including private visibility, **Only select repositories**,
preselection limits, host differences, and recovery.

Setup verifies the exact selected repository membership with a short-lived App
installation token and does not accept a manual-verification fallback. The
write App defaults to the control repository so review outputs have an approved
destination. Ensure every dispatched worker admits the write App's
`APP-SLUG[bot]` login.

## Validate and Commit

```bash
gh cao doctor
gh aw doctor --repo "$CONTROL_REPO" --dir .
git diff --check
```

Follow [Publish and open your dashboard](setup-quickstarts.md#step-3---publish-and-open-your-dashboard).
Guided publication requires separate approval; declining or unattended setup
leaves files local. Review and stage only CAO files, never unrelated changes or
secrets. Publication is not proof that collection or deployment has completed.

Credential setup is complete when the policy parses, exact App installation
membership is verified, the expected App variables and secrets exist, and
`gh aw doctor` reports no blocking installation error. Before live activation,
complete the current-revision dashboard, review, and live checks in
[Validate before activation](control-plane-authentication.md#validate-before-activation).

## Next: Add a Campaign

Campaign selection, installation, enablement, and the first review run are a separate change. Use [Browse campaigns](catalog.md) or ask an agent to use the `central-agentic-ops` skill to add a campaign.
