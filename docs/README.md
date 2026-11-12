---
title: Central Agentic Ops (CAO)
description: Monitor GitHub Agentic Workflow runs, costs, and outcomes. Control execution scope and rollout policy.
template: splash
editUrl: false
prev: false
next: false
head:
  - tag: title
    content: Central Agentic Ops (CAO)
  - tag: meta
    attrs:
      property: og:type
      content: website
agent:
  type: overview
  prominent: true
hero:
  title: Monitor and govern GitHub Agentic Workflows at scale.
  tagline: Keep track of all runs, costs, and outcomes of your GitHub Agentic Workflows across repositories. Set execution scope and rollout policy, refine workflows in review mode, and roll out with confidence.
  actions:
    - text: Explore CAO
      link: /gh-cao/architecture-at-a-glance/
      icon: right-arrow
    - text: Get started
      link: /gh-cao/setup-quickstarts/
      variant: secondary
      icon: right-arrow
    - text: Sample Dashboard
      link: https://githubnext.github.io/gh-cao/cao
      variant: secondary
      icon: right-arrow
---

## Start locally, without a cloud deployment

Use your locally authenticated GitHub CLI and the installed CAO release:

```bash
gh auth status
gh extension install githubnext/gh-cao
gh cao dashboard --repo OWNER/REPOSITORY
```

Open the **Dashboard:** URL printed in your terminal. The bundled server serves
on loopback and queries canonical PostgreSQL cached on your machine, automatically
collecting and refreshing bounded recent evidence from GitHub.
No cloud, Azure, Redis, Node.js or gh-aw setup is needed. Docker/Podman provides
PostgreSQL unless a connection is specified. Data binds to the ignored
`.github/cao/dashboard/` cache.
GitHub authentication and online access are still needed for collection.
Press **Ctrl-C** to stop; the cache remains.

Follow the [local dashboard quickstart](setup-quickstarts.md#open-a-local-dashboard-no-deployment)
for scope, limits, and cache reuse. Shared monitoring and automation can come later.

<!-- The desktop header's Start slideshow button presents this landing page.
Use arrow keys, Page Up/Down, Home/End, or the navigation buttons; Escape exits.
Drawing tools annotate slides, and code snippets or dashboard previews expand
on click. Code edits and annotations are discarded when the presentation closes.
The runtime is adapted from github/gh-aw at
9bf555c835e3a2086c4448de45e75ecc803e6950 under the root MIT license. -->
