---
title: WebMCP
description: Let browser agents discover and read dashboard pages through generated, read-only WebMCP tools.
---

WebMCP lets a browser-based AI agent use the dashboard the way you do. Each
agent-facing dashboard page is exposed as a read-only tool that the agent can
discover and run, so it can answer a question about your control plane without
scraping the page or reconstructing your queries.

The dashboard page definitions are the source of truth. Human rendering and
WebMCP are both generated adapters over those definitions and over the same
query execution path, so there is no second tool catalog to maintain and no way
for the two to disagree.

WebMCP is experimental and available only in browsers that implement it. The
dashboard treats it as progressive enhancement: when the API is missing, the
dashboard behaves exactly as it always has.

## About the generated tools

Every agent-facing page becomes one tool named `cao_<page id>`. A page is agent
facing when it appears in the declared navigation, or when it is a detail page
addressed by a single route parameter. Pages that exist only as navigation
targets of another page are left out, which keeps the catalog bounded.

The mapping from a page definition to a tool is deterministic:

| Page definition | Tool |
| --- | --- |
| `id` | Tool name, prefixed with `cao_` |
| `title` | Tool title |
| `description` or `intent` | Tool description |
| `route.hash-query-parameter` | Required string input |
| `form.fields` | Input schema properties |

Form controls map to JSON Schema types:

| Control | Input type |
| --- | --- |
| `text` | String |
| `checkbox` | Boolean |
| `radio`, `select` | String enum |
| `slider` | Number, bounded by the declared minimum and maximum |

A generated descriptor looks like this:

```json
{
  "name": "cao_campaign_detail",
  "title": "Campaign",
  "description": "Operational activity for this campaign.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "campaign": {
        "type": "string",
        "description": "The campaign this page reports on."
      }
    },
    "required": ["campaign"],
    "additionalProperties": false
  },
  "annotations": { "readOnlyHint": true, "untrustedContentHint": true }
}
```

## About tool execution

Running a tool does exactly what opening the page does, and nothing more:

1. **The arguments are validated.** Undeclared names, wrong types, out-of-range
   numbers, values outside a declared enum, and missing required values are all
   rejected before any query runs. The agent gets an error it can correct.
2. **The dashboard navigates to the page.** The agent and anyone watching the
   screen stay on the same page, looking at the same data.
3. **The page projection is read.** The tool reads through the same page source
   loader and data Web Worker boundary the rendered page uses, so the tool and
   the view cannot return different answers.
4. **A bounded result is returned.** The agent receives JSON with each logical
   source, its availability, completeness, freshness, and `as-of` timestamp, the
   total row count, and a capped sample of rows.

Tools never write. There is no path from a tool call to a repository change, a
dispatch, or a command-line action.

## About safety

Dashboard rows carry text that Central Agentic Ops ingested from GitHub and from
agentic workflow runs: issue titles, workflow names, run titles, firewall
domains. Anyone who can open a pull request can influence that text, so the
dashboard treats it as untrusted wherever it reaches an agent.

- **Results are marked as untrusted.** Every tool declares
  `untrustedContentHint`, and every result is prefixed with the same
  untrusted-context notice the dashboard uses when it hands row JSON to an
  agent. An agent must treat a result as data, never as instructions.
- **Results are reduced to safe values.** Rows are flattened to scalars, plus
  link addresses that clear the same HTTPS safety bar the renderer applies
  before a URL reaches the page. A poisoned record cannot hand an agent a
  `data:`, plaintext, or credential-bearing URL that the rendered page would
  have dropped.
- **Arguments cannot reach code or markup.** Arguments become values inside
  declarative query predicates, never query structure, and they reach the page
  only as text.
- **Navigation stays on the page.** A tool can only move the dashboard to
  `#page-<page id>` with encoded parameters. It cannot change the scheme, the
  origin, or the page you are on.
- **Tools stay same-origin by default.** The dashboard does not broaden tool
  exposure to cross-origin frames.

For the safety rules that govern the rest of the dashboard, see
[Execution and safety](execution-and-safety.md).

## Browser support

Support is detected at runtime, never inferred from the user agent string. The
dashboard checks for `document.modelContext.registerTool` and registers tools
only if it is there.

| Browser | What happens |
| --- | --- |
| Chromium-based browser with WebMCP enabled | Tools are registered |
| Any browser without the API | The dashboard behaves exactly as before |

Nothing is registered when the API is absent, when the feature is turned off, or
when an origin trial has expired. The dashboard ships no polyfill.

## Try it out

WebMCP requires a secure context, so use `https://` or `localhost`.

1. Use a Chromium-based browser that supports WebMCP and turn on the WebMCP
   testing flag at `chrome://flags/#enable-webmcp-testing`, then restart the
   browser.
2. Open a dashboard. For real data, select a successful Activity run and
   download its artifact with Actions read access. From a catalog checkout:

   ```bash
   gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao
   npm run dashboard:local -- --sqlite ../.cao/gh-aw-logs.sqlite
   ```

   Relative Go paths resolve from `server/`. Pages is only the synthetic
   enterprise demo, not an operational snapshot source. To explore WebMCP with
   that sample, use `npm run dashboard:pages:build` followed by
   `npm run dashboard:pages -- --base-path /gh-cao/`.

3. Open the browser developer tools and list the registered tools:

   ```js
   const tools = await document.modelContext.getTools();
   tools.map((tool) => tool.name);
   ```

4. Run a tool the way an agent would:

   ```js
   const cost = tools.find((tool) => tool.name === 'cao_cost');
   const result = await document.modelContext.executeTool(cost, {});
   console.log(result.content[0].text);
   ```

5. Run a tool for a page that takes a parameter. The dashboard navigates to the
   campaign and returns that campaign's projection:

   ```js
   const campaign = tools.find((tool) => tool.name === 'cao_campaign_detail');
   await document.modelContext.executeTool(campaign, { campaign: 'self-care' });
   ```

If `document.modelContext` is `undefined`, the browser does not have WebMCP
available. The dashboard still works normally.

## Add a page to the catalog

You do not register tools by hand. Add an agent-facing page to the dashboard
definition and its tool is generated with it.

When you add a page, keep the agent in mind:

- **Write a description that says what the page answers.** It becomes the tool
  description, and it is what the agent reads when choosing a tool.
- **Label every form field.** Labels become the input descriptions that help the
  agent supply sensible values.
- **Keep the catalog deliberate.** Every registered tool consumes an agent's
  context window, and a large catalog makes tool selection less reliable. Add
  agent-facing pages because an operator needs them, not to raise the count.

WebMCP is one adapter over the shared agent catalog of pages and queries. Agents
with a shell, and agents with neither a browser nor a shell, reach the same
catalog through the `cao` CLI and the read-only MCP server described in
[Agent analysis](agent-analysis.md).

To learn how pages, forms, and queries are declared, see
[Dashboard Language](dashboard-language.md) and the
[Dashboard Language Specification](dashboard-language-specification.md).
