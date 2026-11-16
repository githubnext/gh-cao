# Setup authentication

Load this reference only when selecting or troubleshooting an authentication
profile after exact repository scope is known.

| Verified scope | Compatible profile |
| --- | --- |
| Private or privileged repositories under one organization | `github-app` |
| Repositories across organizations in one GitHub Enterprise Cloud enterprise | `enterprise-app` |
| Approved owner-scoped credentials where Apps are unavailable | `token` (one pair per resource owner) |

The selected profile must cover the full read scope; a multi-owner token setup
does this with a separate pair for each owner. Prefer private GitHub Apps for
durable automation. An organization App cannot cover another owner, and
enterprise Apps require every selected organization to belong to the same
enterprise. Verify that condition before choosing `enterprise-app`. Use
independent control planes only when no approved profile can cover the full
scope.

Use owner-scoped fine-grained PAT pairs only when organization policy permits
them, the required APIs support them, and Apps are unavailable. The operator
must have access to every selected repository and approve one read/write pair
per resource owner. Never use a classic PAT.

Preview App or PAT setup before mutation where the command supports `--dry-run`.
Private keys and tokens must enter only through GitHub or a secure interactive
prompt, never files, chat, arguments, workflow inputs, or commits.

Prefer reusing an approved private read/write App pair before registering new
Apps or offering a PAT fallback. Guided setup offers **Reuse approved private
GitHub Apps**; the direct command is `gh cao setup auth github-app --repo
OWNER/CONTROL --read-client-id READ-ID --write-client-id WRITE-ID`, with
`--dry-run` for preview. It authenticates each App with a hidden key prompt and
verifies selected-repository membership without organization-wide installation
discovery by the CLI user. Repository administrators can add their repositories
to existing approved installations, including Apps with organization
permissions, unless organization policy restricts this to owners. Initial
installation approval and access to App keys remain separate requirements.
On GHES, this installation change requires 3.24 or later.

Do not add `repository_creation` or `installation_repositories` permissions to
CAO runtime Apps or PATs. These are separate provisioning capabilities, not
campaign authority, and setup does not automatically enroll repositories.

Target-repository authentication and model-provider authentication are
independent. A GitHub App or PAT does not authenticate Copilot or another model
provider. Organization billing checks are optional during setup and must not
become a prerequisite when the operator cannot inspect billing.

For exact commands, permissions, variables, secrets, multi-owner mapping, and
data-residency behavior, use
[Control Plane Authentication Profiles](https://github.com/githubnext/gh-cao/blob/main/docs/control-plane-authentication.md).
