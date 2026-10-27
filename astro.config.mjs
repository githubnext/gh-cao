import { defineConfig } from "astro/config";
import { unified } from "@astrojs/markdown-remark";
import starlight from "@astrojs/starlight";
import mermaid from "astro-mermaid";
import starlightGitHubAlerts from "starlight-github-alerts";
import starlightLlmsTxt from "starlight-llms-txt";
import starlightAgent from "./docs/plugins/starlight-agent/index.mjs";
import rewriteDocsLinks from "./docs/rewrite-docs-links.mjs";

export default defineConfig({
  site: "https://githubnext.github.io",
  base: "/gh-cao",
  redirects: {
    "/how-cao-works": "/gh-cao/architecture-at-a-glance/#how-it-works",
    "/deployment-actions-github-app": "/gh-cao/control-plane-authentication/#configure-private-github-apps",
    "/deployment-actions-pat": "/gh-cao/control-plane-authentication/#configure-a-fine-grained-token",
    "/getting-started": "/gh-cao/setup-quickstarts/",
    "/setup": "/gh-cao/setup-quickstarts/",
  },
  srcDir: "./docs",
  markdown: {
    processor: unified({
      remarkPlugins: [[rewriteDocsLinks, { base: "/gh-cao" }]],
    }),
  },
  integrations: [
    mermaid(),
    starlight({
      title: "Central Agentic Ops",
      description: "Enterprise control planes for GitHub Agentic Workflows.",
      logo: {
        light: "./docs/assets/logo-day.svg",
        dark: "./docs/assets/logo-night.svg",
        alt: "",
      },
      favicon: "/favicon.svg",
      customCss: ["./docs/styles/branding.css"],
      plugins: [
        starlightGitHubAlerts(),
        starlightAgent({
          projectName: "Central Agentic Ops",
          description: "Machine-readable navigation for the authoritative CAO documentation and dashboard architecture.",
          repository: "githubnext/gh-cao",
        }),
        starlightLlmsTxt({
          projectName: "Central Agentic Ops",
          description: "Run, observe, and evolve governed GitHub Agentic Workflow campaigns from a central control plane.",
          details: "Start with the high-value routes below. Markdown and Starlight source documents are authoritative; the llms*.txt files are generated during every documentation build.",
          promote: [
            "index",
            "architecture-at-a-glance",
            "setup-quickstarts",
          ],
          demote: [
            "operational-value/reports/**",
            "activity-cache-compression-analysis",
            "dashboard-overview-*",
            "dashboard-view-catalog",
            "operational-observability-visualization-specification",
            "dashboard-language-specification",
          ],
          exclude: [
            "activity*",
            "admission",
            "admission-gate",
            "agent-*",
            "agentic-*",
            "architecture",
            "authentication",
            "authority-envelopes",
            "author-*",
            "cao-cli",
            "configuration",
            "control-*",
            "dashboard*",
            "deployment*",
            "execution-*",
            "glossary",
            "local-debugging",
            "marketplace",
            "operational-*",
            "operations",
            "orchestrators-*",
            "rollout-*",
            "setup-compatibility",
            "setup-fine-*",
            "setup/quickstart",
            "setup-multiple-*",
            "setup-one-*",
          ],
          optionalLinks: [
            {
              label: "Set up a CAO control plane",
              url: "https://github.com/githubnext/gh-cao/blob/main/skills/setup-cao/SKILL.md",
              description: "Use setup-cao for bootstrap, repository scope, and authentication.",
            },
            {
              label: "Debug a CAO failure",
              url: "https://github.com/githubnext/gh-cao/blob/main/skills/debug-cao/SKILL.md",
              description: "Use debug-cao for policy, credential, workflow, Activity, dashboard, or safe-output failures.",
            },
            {
              label: "Add an existing campaign",
              url: "https://github.com/githubnext/gh-cao/blob/main/skills/add-cao-campaign/SKILL.md",
              description: "Use add-cao-campaign to discover, compare, and install a catalog campaign.",
            },
            {
              label: "Create a new campaign",
              url: "https://github.com/githubnext/gh-cao/blob/main/skills/create-cao-campaign/SKILL.md",
              description: "Use create-cao-campaign to author an orchestrator and its workers.",
            },
            {
              label: "Analyze CAO activity",
              url: "https://github.com/githubnext/gh-cao/blob/main/skills/analyze-cao/SKILL.md",
              description: "Use analyze-cao to answer questions from a published Activity snapshot.",
            },
            {
              label: "Use or extend the CAO CLI",
              url: "https://github.com/githubnext/gh-cao/blob/main/skills/cao-cli/SKILL.md",
              description: "Use cao-cli for command selection, workflow invocation, and CLI development.",
            },
            {
              label: "Compact project context",
              url: "https://githubnext.github.io/gh-cao/llms-small.txt",
              description: "Load only when a routed skill needs project context.",
            },
            {
              label: "Dashboard data access",
              url: "https://githubnext.github.io/gh-cao/cao/llms.txt",
              description: "Choose bounded dashboard artifacts, MCP queries, or local SQLite analysis.",
            },
            {
              label: "Machine-readable resource index",
              url: "https://githubnext.github.io/gh-cao/agent/resources.json",
              description: "Discover every documentation route without loading its body.",
            },
            {
              label: "Scoped documentation index",
              url: "https://githubnext.github.io/gh-cao/agent/llms.txt",
              description: "Browse the small set of important HTML and JSON resources.",
            },
          ],
        }),
      ],
      markdown: {
        processedDirs: ["."],
      },
      components: {
        Footer: "./docs/components/SiteFooter.astro",
        Hero: "./docs/components/HierarchyHero.astro",
        SocialIcons: "./docs/components/HeaderLinks.astro",
        ThemeSelect: "./docs/components/ThemeToggle.astro",
      },
      editLink: {
        baseUrl: "https://github.com/githubnext/gh-cao/edit/main/",
      },
      head: [
        {
          tag: "meta",
          attrs: {
            property: "og:image",
            content: "https://githubnext.github.io/gh-cao/social-card.png",
          },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:type", content: "image/png" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:width", content: "1200" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:height", content: "630" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:alt", content: "Central Agentic Ops - monitor and govern GitHub Agentic Workflows." },
        },
        {
          tag: "meta",
          attrs: {
            name: "twitter:image",
            content: "https://githubnext.github.io/gh-cao/social-card.png",
          },
        },
        {
          tag: "meta",
          attrs: { name: "twitter:image:alt", content: "Central Agentic Ops - monitor and govern GitHub Agentic Workflows." },
        },
        {
          // Starlight renders wide markdown tables and code blocks as horizontally
          // scrollable regions (`overflow: auto`), but they aren't keyboard focusable,
          // so keyboard users can't reach clipped content (axe
          // `scrollable-region-focusable`, WCAG 2.1.1/2.1.3). Make overflowing
          // regions focusable with an accessible name.
          tag: "script",
          content: `(function () {
            function findPrecedingHeadingText(headings, table) {
              let text = null;
              for (const heading of headings) {
                if (heading.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING) {
                  text = heading.textContent.trim();
                } else {
                  break;
                }
              }
              return text;
            }
            function markScrollableRegions() {
              document.querySelectorAll(".sl-markdown-content").forEach((content) => {
                const headings = [...content.querySelectorAll("h1, h2, h3, h4, h5, h6")];
                const seenLabels = new Map();
                let unlabeledCount = 0;
                content.querySelectorAll("table, pre").forEach((region) => {
                  if (region.scrollWidth <= region.clientWidth) return;
                  if (!region.hasAttribute("tabindex")) region.setAttribute("tabindex", "0");
                  if (region.hasAttribute("aria-label") || region.hasAttribute("aria-labelledby")) return;
                  const headingText = findPrecedingHeadingText(headings, region);
                  const labelPrefix = region.matches("pre") ? "Scrollable code example" : "Scrollable table";
                  let label;
                  if (headingText) {
                    const labelKey = \`\${labelPrefix}:\${headingText}\`;
                    const count = (seenLabels.get(labelKey) || 0) + 1;
                    seenLabels.set(labelKey, count);
                    label = count > 1 ? \`\${labelPrefix}: \${headingText} (\${count})\` : \`\${labelPrefix}: \${headingText}\`;
                  } else {
                    unlabeledCount += 1;
                    label = \`\${labelPrefix} \${unlabeledCount}\`;
                  }
                  region.setAttribute("aria-label", label);
                });
              });
            }
            if (document.readyState === "loading") {
              document.addEventListener("DOMContentLoaded", markScrollableRegions);
            } else {
              markScrollableRegions();
            }
            window.addEventListener("resize", markScrollableRegions);
            document.addEventListener("astro:page-load", markScrollableRegions);
          })();`,
        },
      ],
      social: [
        {
          icon: "github",
          label: "GitHub repository",
          href: "https://github.com/githubnext/gh-cao",
        },
      ],
      sidebar: [
        {
          label: "Introduction",
          items: [
            { label: "What is Central Agentic Ops?", link: "/architecture-at-a-glance/" },
            { label: "Architecture", link: "/architecture/" },
          ],
        },
        {
          label: "Setup",
          items: [
            { label: "Quick Start", link: "/setup-quickstarts/" },
            { label: "Provision Credentials", link: "/control-plane-authentication/" },
            { label: "Setup Compatibility", link: "/setup-compatibility/" },
            { label: "Set Up the Admission Gate", link: "/admission-gate/" },
            { label: "CLI Commands", link: "/cao-cli/" },
          ],
        },
        {
          label: "Gallery",
          items: [
            { label: "Campaign Catalog", link: "/catalog/" },
            { label: "Daily Repo Status", link: "/setup/quickstart/" },
          ],
        },
        {
          label: "Guides",
          items: [
            { label: "From Outcome to Execution", link: "/operation-patterns/" },
            { label: "Safe Rollout", link: "/rollout-and-routing/" },
            { label: "Measuring Operational Value", link: "/operational-value-how-to/" },
            { label: "Agent Analysis", link: "/agent-analysis/" },
            { label: "Copilot Plugin and Canvas", link: "/agent-plugin/" },
            { label: "Deployment Options", link: "/deployment/" },
            { label: "Deployment (GitHub Actions)", link: "/deployment-actions/" },
            { label: "Deployment (Azure)", link: "/deployment-azure/" },
            { label: "Deployment (Upstash Redis)", link: "/deployment-upstash/" },
            { label: "Deployment (Managed Redis)", link: "/deployment-managed-redis/" },
          ],
        },
        {
          label: "Dashboard",
          items: [
            { label: "Overview", link: "/dashboard/" },
            { label: "Intelligence", link: "/intelligence/" },
            { label: "Runtime Health", link: "/computation-runtime-health/" },
            { label: "Portfolio Decisions", link: "/computation-intelligence/" },
            { label: "Data Ingestion", link: "/dashboard-data-ingestion/" },
            { label: "Data Model", link: "/dashboard-data-model/" },
            { label: "Dashboard Language", link: "/dashboard-language/" },
          ],
        },
        {
          label: "Reference",
          items: [
            { label: "Admission Gates", link: "/admission/" },
            { label: "Agentic Workflow Smells", link: "/agentic-workflow-smells/" },
            { label: "Authentication", link: "/authentication/" },
            { label: "Authority Envelopes", link: "/authority-envelopes/" },
            { label: "CAO Activity", link: "/activity/" },
            { label: "Campaign Package Marketplace", link: "/marketplace/" },
            { label: "Configuration", link: "/configuration/" },
            { label: "Deployment and Governance", link: "/deployment-and-governance/" },
            { label: "Execution and Safety", link: "/execution-and-safety/" },
            { label: "Glossary", link: "/glossary/" },
            { label: "Operational Value", link: "/operational-value/" },
            { label: "Orchestrators and Workers", link: "/orchestrators-and-workers/" },
          ],
        },
        {
          label: "Troubleshooting",
          collapsed: true,
          items: [
            { label: "Monitor and Recover", link: "/operations/" },
            { label: "Local Worker Debugging", link: "/local-debugging/" },
          ],
        },
        { label: "Emergency Stop", link: "/operations/#emergency-stop" },
        { label: "Sample Dashboard", link: "/cao/" },
      ],
    }),
  ],
});