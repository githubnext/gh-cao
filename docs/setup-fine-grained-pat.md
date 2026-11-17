---
title: Set Up with Fine-Grained PATs
description: Install a CAO control plane with separate owner-scoped tokens when your repository scope and permissions require them.
---

Use this path when a suitable GitHub App is unavailable and you meet the
[PAT eligibility checks](authentication.md#fine-grained-pat-fallback):
existing repository access, organization approval, supported APIs, exact
owner-scoped selections, and manual expiration/rotation. Never use a classic PAT.

> [!IMPORTANT]
> Existing repository access does not authorize token setup. A coding agent must show the dry-run plan and ask before opening PAT creation pages, storing tokens or repository maps, committing, or pushing.

## Install

Complete [Step 1 of Set Up CAO](setup-quickstarts.md#step-1---install-cao):
install the extension and enter a clean default-branch control checkout.
That guide covers creating versus cloning the repository and the
[SAML installer fallback](cao-cli.md#control-plane-installer).
From the control checkout:

```bash
CONTROL_REPO="acme/central-agentic-ops"
TARGET_REPO="acme/example-service"

gh cao setup
```

At **Repositories**, enter the exact value of `$TARGET_REPO`; the control
repository stays included. Choose **`token`** when prompted.
Setup prepares Activity, Dashboard, validation, shared runtime and missing
baseline policy, then guides scoped token creation and Pages.
A private repository does not guarantee private Pages: require restricted
access for non-public evidence and decline public publication otherwise.

## Configure the Tokens

Guided setup shows each owner-scoped token plan before requesting a secret.
For credential-only changes after setup, inspect a dry run before applying:

```bash
gh cao setup auth token \
  --repo "$CONTROL_REPO" \
  --write-repository "$CONTROL_REPO" \
  --expires-in 30 \
  --dry-run

gh cao setup auth token \
  --repo "$CONTROL_REPO" \
  --write-repository "$CONTROL_REPO" \
  --expires-in 30
```

Follow [Configure a fine-grained token](control-plane-authentication.md#configure-a-fine-grained-token)
for each browser form, secure prompt, repository map, and interrupted-setup
recovery. Read scope and approved output scope remain separate; token setup does
not enable a campaign.

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

Credential setup is complete when the policy parses, the owner-scoped secret
maps match the exact repositories, and `gh aw doctor` reports no blocking
installation error. Before live activation, complete the current-revision
dashboard, review, and live checks in
[Validate before activation](control-plane-authentication.md#validate-before-activation).

## Next: Add a Campaign

Campaign selection, installation, enablement, and the first review run are a separate change. Use [Browse campaigns](catalog.md) or ask an agent to use the `central-agentic-ops` skill to add a campaign.
