# Contributing to Central Agentic Ops

Thank you for helping improve Central Agentic Ops (CAO). Contributions may
include bug reports, documentation, campaign ideas, workflow improvements, and
code changes. By participating, you agree to follow our
[Code of Conduct](CODE_OF_CONDUCT.md).

## Choose the right contribution path

### Community contributors

If you do not have write access, do not open a pull request directly. Search
[existing issues](https://github.com/githubnext/gh-cao/issues) first, then open
an issue with a detailed agentic plan that maintainers can review and implement.

Include:

- the problem, use case, and expected behavior;
- relevant components, files, or documentation;
- a focused implementation plan;
- proposed validation and documentation updates; and
- sanitized examples, logs, or screenshots when useful.

A maintainer may ask for revisions before accepting the plan. Acceptance does
not guarantee implementation or a particular timeline.

### Contributors with write access

Open a draft pull request and keep it in draft while developing and validating
the change. Keep the change focused, explain its intent and safety impact, and
mark it ready only when the applicable checks pass and the documentation is
current.

## Repository expectations

Read [CODEBASE.yml](CODEBASE.yml) for the compact architecture model and
[AGENTS.md](AGENTS.md) for repository conventions and validation commands.

- Preserve CAO's policy and authorization boundaries; do not widen repository
  scope, rollout mode, credentials, or write capabilities implicitly.
- Edit workflow sources in `.github/workflows/*.md`, not generated
  `.github/workflows/*.lock.yml` files.
- Add or update focused tests for behavior changes.
- Run the narrowest applicable checks while iterating. Use `npm run check` for
  complete repository validation.
- Run `npm run compile` after changing workflow Markdown.

<span id="go-extension-development"></span>

## Local extension development

Use the Go version declared in `go.mod`. Run `make check`, then build the
operator extension with `make build VERSION=dev`. Use `./gh-cao` directly from
a differently named worktree; a checkout named `gh-cao` can install the local
build with `gh extension install .` and invoke it as `gh cao`.

Never include credentials, tokens, private repository data, personal
information, or other sensitive data in issues, prompts, logs, examples,
commits, or pull requests.

Report suspected vulnerabilities privately as described in
[SECURITY.md](SECURITY.md), not through a public issue or pull request. For
general help, see [SUPPORT.md](SUPPORT.md).
