# Operate CAO Campaigns and Workflows

Use this guide to choose, install, author, or change an operation in a control
repository that `setup.md` has already initialized. Load only the procedure
that matches the request:

| Request | Read |
| --- | --- |
| Discover, compare, recommend, or install an existing catalog campaign | [Add a campaign](operations/add-campaign.md) |
| Describe an outcome, scope, and execution requirements for new work | [Create a campaign](operations/create-campaign.md) |
| Author or change the gh-aw workflow source itself | [gh aw](gh-aw.md) |

Both procedures start from
[From Outcome to Execution](https://github.com/githubnext/gh-cao/blob/main/docs/operation-patterns.md).
Infer execution structure, never authority: installation, enablement, dispatch,
and `live` mode each require explicit reviewed policy in
`.github/workflows/cao.json` at the exact workflow SHA. Keep workflow and policy
changes in one reviewed commit, and leave new operations in `review`.

Use running-operation evidence from [dashboard analysis](dashboard-analysis.md)
to justify a change, and [diagnostics](diagnostics.md) when a run failed.
