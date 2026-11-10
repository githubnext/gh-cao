---
title: Control Policy
description: Understand where Central Agentic Ops policy is defined and which layer owns each decision.
sidebar:
  order: 1361
---

Use this page when changing campaign enablement, rollout, target authority, or
worker admission. `.github/workflows/cao.json` is the sole persistent non-secret
CAO policy authority: CAO governs rollout and target authority, while gh-aw
governs engine limits, generated job topology, authentication, and safe-output
execution. Next read the
[Central Agentic Ops Control Architecture Specification](https://github.com/githubnext/gh-cao/blob/main/specs/control-architecture.md),
then inspect `.github/workflows/shared/control.md` and its dependencies.

The optional `control-plane.web.host` section composes an app server target module
with an independent Redis provider module and records only non-secret capabilities
and environment-variable references. Keep connection
URLs, passwords, and CA certificate contents in the deployment secret manager.