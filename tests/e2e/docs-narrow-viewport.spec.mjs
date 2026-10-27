import { expect, test } from "@playwright/test";
import { issueOpenedPaths, safeOutputPaths } from "../../docs/components/workflow-illustration-icons.mjs";

const paths = [
  "./",
  "architecture-at-a-glance/",
  "setup-quickstarts/",
  "repo-assist-quickstart/",
  "setup/quickstart/",
  "setup-one-organization/",
  "setup-multiple-organizations/",
  "setup-fine-grained-pat/",
  "cao-cli/",
  "author-your-first-operation/",
  "authentication/",
  "configuration/",
  "operations/",
  "architecture/",
  "dashboard/",
  "catalog/",
  "catalog/dependabot/",
  "operational-observability-visualization-specification/",
  "operational-value-how-to/",
];

for (const width of [320, 1280]) {
  test(`Daily Repo Status quickstart keeps bounded review steps and working links at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    expect((await page.goto("setup/quickstart/"))?.ok()).toBe(true);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Daily Repo Status quickstart");
    const content = page.locator(".sl-markdown-content");
    await expect(content).not.toContainText("Video TODO");
    await expect(content.locator("video, iframe")).toHaveCount(0);
    await expect(content).toContainText("safe_output_mode=\"review\"");
    await expect(content).toContainText("control-plane.workflows");
    await expect(content).toContainText("No campaign, orchestrator or worker dispatch is required");
    await expect(content).toContainText("manual-only");
    await expect(content).toContainText("Operational value is not measurable yet");
    await expect(content.locator("h2")).toHaveText([
      "Prerequisites",
      "Step 1 - Add and review the sample workflow",
      "Step 2 - Request one bounded review",
      "Step 3 - Wait for the run and inspect its result",
      "Step 4 - Customize your report (optional)",
      "Continue learning",
    ]);
    await expect(content).not.toContainText("install-gh-cao.sh");
    await expect(content.getByRole("link", { name: "Set Up CAO", exact: true }))
      .toHaveAttribute("href", "/gh-cao/setup-quickstarts/");
    for (const link of await content.locator('a[href^="/gh-cao/"]').all()) {
      const href = await link.getAttribute("href");
      const response = await page.request.get(href);
      expect(response.ok(), href).toBe(true);
      const hash = new URL(href, page.url()).hash;
      if (hash) expect(await response.text(), href).toContain(`id="${hash.slice(1)}"`);
    }
    expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
  });
}

for (const colorScheme of ["light", "dark"]) {
  for (const width of [320, 1280]) {
    test(`operational value beginner guide connects evidence and research at ${width}px in ${colorScheme}`, async ({ page }) => {
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.setViewportSize({ width, height: 900 });
      expect((await page.goto("operational-value-how-to/"))?.ok()).toBe(true);
      await expect(page.locator("html")).toHaveAttribute("data-theme", colorScheme);
      const content = page.locator(".sl-markdown-content");
      await expect(content.locator(".cao-value-trail")).toHaveCount(0);
      await expect(content.locator(".mermaid")).toHaveCount(1);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText("Define and measure workflow value");
      await expect(content).toContainText("Markdown, imports and precomputes");
      await expect(content).toContainText("value function");
      await expect(content).toContainText("at least 800 lines");
      await expect(content).toContainText("1,129 to 248 lines");
      await expect(content).toContainText("fixed cadence, including periods");
      await expect(content).toContainText("compare cohorts with equal follow-up");
      await expect(content).toContainText("Unresolved work is not automatically zero or failure");
      await expect(content.locator(".mermaid svg")).toBeVisible();
      await expect(content.locator(".mermaid svg")).toContainText("Repository snapshots: primary outcome");
      await expect(content.locator(".mermaid svg")).toContainText("Accepted implementation: merged PR");
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      for (const link of await content.locator('a[href^="/gh-cao/"]').all()) {
        const href = await link.getAttribute("href");
        const response = await page.request.get(href);
        expect(response.ok(), href).toBe(true);
        const hash = new URL(href, page.url()).hash;
        if (hash) {
          const html = await response.text();
          expect(html, href).toContain(`id="${hash.slice(1)}"`);
        }
      }

    });
  }
}

test("operational value beginner guide remains readable without JavaScript", async ({ browser, baseURL }) => {
  const context = await browser.newContext({
    baseURL,
    javaScriptEnabled: false,
    viewport: { width: 320, height: 900 },
  });
  try {
    const page = await context.newPage();
    expect((await page.goto("operational-value-how-to/"))?.ok()).toBe(true);
    const content = page.locator(".sl-markdown-content");
    await expect(content.getByRole("heading", { name: "Define the value function", exact: true })).toBeVisible();
    await expect(content.locator(".cao-value-trail")).toHaveCount(0);
    await expect(content).toContainText("actual first parent");
    await expect(content.getByRole("link", { name: "github/gh-aw#55785", exact: true })).toBeVisible();
    expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(320);
  } finally {
    await context.close();
  }
});

for (const width of [320, 390, 799, 800, 1440, 1920]) {
  test(`documentation uses responsive content width at ${width}px without changing the hero page`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    expect((await page.goto("setup-quickstarts/"))?.ok()).toBe(true);
    await expect(page.locator("html")).not.toHaveAttribute("data-has-hero");
    expect(await page.locator("html").evaluate(
      (element) => getComputedStyle(element).getPropertyValue("--sl-content-width").trim(),
    )).toBe("38.75rem");
    const content = page.locator(".sl-markdown-content");
    const bounds = await content.boundingBox();
    expect(bounds.width).toBeGreaterThan(0);
    expect(bounds.width).toBeLessThanOrEqual(Math.min(width, 620));
    if (width === 1920) expect(bounds.width).toBeCloseTo(620, 0);
    expect(await page.locator("html").evaluate((element) => element.scrollWidth))
      .toBeLessThanOrEqual(width);

    expect((await page.goto("./"))?.ok()).toBe(true);
    await expect(page.locator("html")).toHaveAttribute("data-has-hero", "");
    expect(await page.locator("html").evaluate(
      (element) => getComputedStyle(element).getPropertyValue("--sl-content-width").trim(),
    )).toBe("67.5rem");
  });
}

for (const colorScheme of ["light", "dark"]) {
  for (const [width, sizes] of [
    [390, { body: 15.0334, h1: 27.7739, h2: 19.1892, h3: 16.9817 }],
    [1440, { body: 16, h1: 39.0624, h2: 25, h3: 20 }],
  ]) {
    test(`documentation matches gh-aw prose typography at ${width}px in ${colorScheme}`, async ({ page }) => {
      await page.emulateMedia({ colorScheme });
      await page.setViewportSize({ width, height: 900 });
      expect((await page.goto("configuration/"))?.ok()).toBe(true);
      const typography = await page.evaluate(() => {
        const selectors = {
          body: ".sl-markdown-content > p",
          item: ".sl-markdown-content li",
          h1: "h1#_top",
          h2: ".sl-markdown-content h2",
          h3: ".sl-markdown-content h3",
        };
        return Object.fromEntries(Object.entries(selectors).map(([name, selector]) => {
          const style = getComputedStyle(document.querySelector(selector));
          return [name, {
            fontSize: parseFloat(style.fontSize),
            lineHeight: parseFloat(style.lineHeight),
            fontWeight: style.fontWeight,
          }];
        }));
      });
      for (const name of ["body", "h1", "h2", "h3"]) {
        expect(typography[name].fontSize).toBeCloseTo(sizes[name], 2);
      }
      expect(typography.item.fontSize).toBeCloseTo(sizes.body, 2);
      expect(typography.body.lineHeight).toBeCloseTo(sizes.body * 1.7, 2);
      expect(typography.h1.lineHeight).toBeCloseTo(sizes.h1 * 1.15, 2);
      expect(typography.h2.lineHeight).toBeCloseTo(sizes.h2 * 1.35, 2);
      expect(typography.h1.fontWeight).toBe("400");
      expect(typography.h2.fontWeight).toBe("400");
      expect(typography.h3.fontWeight).toBe("600");
      expect(await page.locator("html").evaluate(element => element.scrollWidth))
        .toBeLessThanOrEqual(width);
    });
  }
}

test("Setup navigation separates onboarding from gallery examples and authentication reference", async ({ page }) => {
  expect((await page.goto("getting-started/"))?.ok()).toBe(true);

  const introductionGroup = page.locator('nav[aria-label="Main"] summary').filter({ hasText: /^Introduction$/ }).locator("..");
  await expect(introductionGroup.getByRole("link")).toHaveText([
    "What is Central Agentic Ops?",
    "Architecture",
  ]);

  const setupGroup = page.locator('nav[aria-label="Main"] summary').filter({ hasText: /^Setup$/ }).locator("..");
  await expect(setupGroup.getByRole("link")).toHaveText([
    "Quick Start",
    "Provision Credentials",
    "Setup Compatibility",
    "Set Up the Admission Gate",
    "CLI Commands",
  ]);

  const galleryGroup = page.locator('nav[aria-label="Main"] summary').filter({ hasText: /^Gallery$/ }).locator("..");
  await expect(galleryGroup.getByRole("link")).toHaveText(["Campaign Catalog", "Daily Repo Status"]);
  const referenceGroup = page.locator('nav[aria-label="Main"] summary').filter({ hasText: /^Reference$/ }).locator("..");
  await expect(referenceGroup.getByRole("link", { name: "Authentication", exact: true }))
    .toHaveAttribute("href", "/gh-cao/authentication/");
});

test("getting started leads from monitoring to a separate optional campaign guide", async ({ page }, testInfo) => {
  expect((await page.goto("setup-quickstarts/"))?.ok()).toBe(true);
  await expect(page.locator("main h2")).toHaveText([
    "Prerequisites",
    "Step 1 - Install CAO",
    "Step 2 - Connect one repository",
    "Step 3 - Publish and open your dashboard",
    "Optional: add automation later",
    "Continue learning",
  ]);
  const content = page.locator(".sl-markdown-content");
  await expect(content).toContainText("Monitoring only: no campaign installation or new agentic work.");
  await expect(content).toContainText("An empty dashboard is expected.");
  await expect(content).not.toContainText("gh aw run");
  await expect(content).toContainText("gh cao GitHub CLI extension");
  await expect(content).toContainText("baseline monitoring runtime and policy");
  await expect(content).not.toContainText("recording not yet available");
  await expect(content.locator("video, iframe")).toHaveCount(0);
  for (const anchor of [
    "guided-setup-instead", "4-open-your-dashboard",
    "step-3---review-and-publish-your-configuration",
    "step-4---wait-for-collection-and-inspect-your-dashboard",
  ]) {
    await expect(content.locator(`[id="${anchor}"]`)).toHaveCount(1);
  }
  for (const link of await content.locator('a[href^="/gh-cao/"]').all()) {
    const href = await link.getAttribute("href");
    const response = await page.request.get(href);
    expect(response.ok(), href).toBe(true);
    const hash = new URL(href, page.url()).hash;
    if (hash) expect(await response.text(), href).toContain(`id="${hash.slice(1)}"`);
  }
  for (const [name, viewport] of [
    ["desktop", { width: 1280, height: 900 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    await page.setViewportSize(viewport);
    const screenshot = testInfo.outputPath(`getting-started-${name}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach(`Getting started (${name})`, { path: screenshot, contentType: "image/png" });
  }
  await content.getByRole("link", { name: "Daily Repo Status quickstart", exact: true }).click();
  await expect(page).toHaveURL(/\/setup\/quickstart\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Daily Repo Status quickstart");
});

test("CAO introduction combines purpose and operating flow and leads to architecture", async ({ page }, testInfo) => {
  expect((await page.goto("architecture-at-a-glance/"))?.ok()).toBe(true);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("What is Central Agentic Ops?");
  const content = page.locator(".sl-markdown-content");
  const text = await content.innerText();
  expect(text.match(/central agentic ops/gi)).toHaveLength(1);
  expect(text.trim().split(/\s+/u).length).toBeLessThanOrEqual(425);
  await expect(content.locator("p").first()).toContainText("Central Agentic Ops (CAO)");
  await expect(content.locator("p").first()).toContainText("a system for monitoring and governing GitHub Agentic Workflows (AWs) across repositories");
  const introduction = content.locator("p").filter({ hasText: /^Agentic work might begin/ });
  await expect(introduction).toHaveText(/^Agentic work might begin with a single task/);
  await expect(introduction.locator("strong")).toHaveCount(0);
  await expect(content).toContainText("AWs run those tasks in GitHub Actions");
  await expect(content).toContainText("These AWs form the building blocks of CAO as we scale.");
  await expect(content).toContainText("Monitoring existing workflows does not move them or change their permissions.");
  await expect(content).toContainText("CAO governs explicitly integrated workflows, not every observed AW");
  await expect(content).toContainText("A successful run is not automatically a successful outcome.");
  await expect(content.locator("h2")).toHaveText([
    "How it works",
    "A simple example",
  ]);
  const capabilities = content.getByRole("list").filter({ hasText: "Observe activity" });
  await expect(capabilities.locator(":scope > li")).toHaveCount(3);
  await expect(content.locator("details")).toHaveCount(0);
  await expect(capabilities).toContainText("Observe activity");
  await expect(capabilities).toContainText("Evaluate results");
  await expect(capabilities).toContainText("Govern shared work");
  await expect(content).toContainText("live output requires explicit authorization");
  await expect(content.locator("table, pre, .mermaid")).toHaveCount(0);
  await expect(content.getByRole("heading", { name: "A simple example", exact: true })).toBeVisible();
  await expect(content).toContainText("Campaigns are one way to package and scale work, not a prerequisite.");
  await expect(content).toContainText("read-only snapshot");
  await expect(content).toContainText("Missing evidence is not a healthy zero.");
  await expect(content).toContainText("Review is real execution");
  await expect(content).toContainText("Missing required authority, access, or evidence fails closed.");
  await expect(content.getByRole("heading", { name: /campaign/i })).toHaveCount(0);
  const campaignLink = content.getByRole("link", { name: "Browse campaigns", exact: true });
  await expect(campaignLink).toHaveAttribute("href", "/gh-cao/catalog/");
  await expect(campaignLink).toHaveCount(1);
  const image = content.getByAltText(/^CAO Repositories chart view/);
  await image.scrollIntoViewIfNeeded();
  await expect(image).toBeVisible();
  await expect.poll(() => image.evaluate((element) => element.complete && element.naturalWidth > 0)).toBe(true);
  await expect(content.locator("figure figcaption")).toContainText("Collected evidence, not a live feed.");
  await expect(content.getByRole("link", { name: "Explore the public Repositories view", exact: true }))
    .toHaveAttribute("href", "https://githubnext.github.io/gh-cao/cao/#page-repositories");

  const setupLinks = content.getByRole("link", { name: "Set up CAO and monitor your workflows", exact: true });
  await expect(setupLinks).toHaveCount(1);
  expect(await setupLinks.last().evaluate((element) =>
    Boolean(element.compareDocumentPosition(document.querySelector('main a[href="/gh-cao/catalog/"]')) & Node.DOCUMENT_POSITION_FOLLOWING),
  )).toBe(true);
  await expect(setupLinks.first()).toHaveAttribute("href", "/gh-cao/setup-quickstarts/");
  const next = page.locator(".pagination-links a[rel=next]");
  await expect(next).toContainText("Architecture");
  await expect(next).toHaveAttribute("href", "/gh-cao/architecture/");

  for (const [name, viewport] of [
    ["desktop", { width: 1280, height: 900 }],
    ["mobile", { width: 390, height: 844 }],
    ["narrow-mobile", { width: 320, height: 844 }],
  ]) {
    await page.setViewportSize(viewport);
    expect(await page.locator("html").evaluate((element) => element.scrollWidth)).toBeLessThanOrEqual(viewport.width);
    const imageBounds = await image.boundingBox();
    expect(imageBounds.width).toBeGreaterThan(0);
    expect(imageBounds.x).toBeGreaterThanOrEqual(0);
    expect(imageBounds.x + imageBounds.width).toBeLessThanOrEqual(viewport.width);
    const screenshot = testInfo.outputPath(`cao-introduction-${name}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach(`CAO introduction (${name})`, { path: screenshot, contentType: "image/png" });
  }

  for (const link of await content.locator('a[href^="/gh-cao/"]').all()) {
    const response = await page.request.get(await link.getAttribute("href"));
    expect(response.ok(), response.url()).toBe(true);
  }
  await next.click();
  await expect(page).toHaveURL(/\/architecture\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Architecture");
});

test("architecture retains authority and evidence boundaries without a duplicate introduction", async ({ page }) => {
  expect((await page.goto("architecture/"))?.ok()).toBe(true);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Architecture");
  await expect(page.locator(".sl-markdown-content"))
    .toContainText("orchestrators and workers run from the control repository");
  for (const id of [
    "objectives", "control-plane-and-dashboard", "mental-model", "repository-roles-and-records",
    "authority-and-admission", "how-it-works", "execution-boundaries", "evidence-and-dashboard-hosting",
    "core-safety-properties", "detailed-references",
  ]) {
    await expect(page.locator(`[id="${id}"]`)).toHaveCount(1);
  }
  await expect(page.locator(".pagination-links a[rel=prev]"))
    .toHaveAttribute("href", "/gh-cao/architecture-at-a-glance/");
  const architecture = page.locator(".sl-markdown-content");
  await expect(architecture).toContainText(".github/workflows/cao.json is the sole persistent non-secret rollout policy");
  await expect(architecture).toContainText("target files do not grant, narrow, or veto control-plane authority");
  await expect(architecture).toContainText("not the policy credentials used for precompute");
  await expect(architecture).toContainText("It does not fall back to browser storage or static shards");
  await expect(architecture).toContainText("A private repository does not guarantee private Pages");
  for (const link of await architecture.locator('a[href^="/gh-cao/"]').all()) {
    const href = await link.getAttribute("href");
    const response = await page.request.get(href);
    expect(response.ok(), href).toBe(true);
    const hash = new URL(href, page.url()).hash;
    if (hash) expect(await response.text(), href).toContain(`id="${hash.slice(1)}"`);
  }
});

test("primary navigation follows gh-aw categories without duplicating CAO content", async ({ page }, testInfo) => {
  await page.goto("dashboard/");
  const sidebar = page.locator("#starlight__sidebar");
  await expect(sidebar.locator("summary")).toHaveText([
    "Introduction", "Setup", "Gallery", "Guides", "Dashboard", "Reference", "Troubleshooting",
  ]);
  await expect(sidebar.locator('.top-level a[href^="/gh-cao/"]')).toHaveCount(42);
  await expect(sidebar.getByText("Maintain", { exact: true })).toHaveCount(0);
  await expect(sidebar.getByText("Views", { exact: true })).toHaveCount(0);
  const troubleshooting = sidebar.locator("summary").filter({ hasText: /^Troubleshooting$/ }).locator("..");
  await expect(troubleshooting).toHaveJSProperty("open", false);
  const emergencyStop = sidebar.getByRole("link", { name: "Emergency Stop", exact: true });
  await expect(emergencyStop).toBeVisible();
  await expect(emergencyStop.locator("xpath=ancestor::details")).toHaveCount(0);
  await expect(emergencyStop).toHaveAttribute("href", "/gh-cao/operations/#emergency-stop");
  await troubleshooting.locator("summary").click();
  await expect(troubleshooting.getByRole("link", { name: "Local Worker Debugging", exact: true }))
    .toHaveAttribute("href", "/gh-cao/local-debugging/");
  const guides = sidebar.locator("summary").filter({ hasText: /^Guides$/ }).locator("..");
  for (const route of ["operation-patterns", "rollout-and-routing", "operational-value-how-to", "deployment", "agent-plugin"]) {
    await expect(guides.locator(`a[href="/gh-cao/${route}/"]`)).toHaveCount(1);
  }
  await expect(sidebar.getByRole("link", { name: "Sample Dashboard", exact: true }))
    .toHaveAttribute("href", "/gh-cao/cao/");
  for (const route of [
    "how-cao-works", "deployment-actions-github-app", "deployment-actions-pat",
    "dashboard-language-specification", "dashboard-view-catalog",
  ]) {
    await expect(sidebar.locator(`a[href="/gh-cao/${route}/"]`)).toHaveCount(0);
  }
  const content = page.locator(".sl-markdown-content");
  for (const route of ["dashboard-language-specification", "dashboard-view-catalog", "dashboard-overview"]) {
    await expect(content.locator(`a[href="/gh-cao/${route}/"]`)).toHaveCount(1);
  }

  await troubleshooting.locator("summary").click();
  await page.evaluate(() => document.fonts.ready);
  for (const [name, viewport] of [
    ["desktop", { width: 1280, height: 900 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    await page.setViewportSize(viewport);
    if (viewport.width < 800) {
      await page.getByRole("button", { name: "Menu", exact: true }).click();
    }
    await expect(sidebar).toBeVisible();
    await sidebar.locator("summary").first().scrollIntoViewIfNeeded();
    expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(viewport.width);
    const screenshot = testInfo.outputPath(`cao-navigation-${name}.png`);
    await page.screenshot({ path: screenshot });
    await testInfo.attach(`CAO navigation (${name})`, { path: screenshot, contentType: "image/png" });
  }
});

test("removed guide URLs including old fragments redirect without JavaScript", async ({ browser, baseURL }) => {
  const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    for (const [legacy, destination, heading] of [
      ["how-cao-works/#review-promote-and-recover", "architecture-at-a-glance/#how-it-works", "What is Central Agentic Ops?"],
      ["deployment-actions-github-app/#validating-the-credentials", "control-plane-authentication/#configure-private-github-apps", "Control Plane Authentication Profiles"],
      ["deployment-actions-pat/#configuration-reference", "control-plane-authentication/#configure-a-fine-grained-token", "Control Plane Authentication Profiles"],
    ]) {
      await page.goto(legacy);
      await expect(page).toHaveURL(new URL(destination, baseURL).href);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(heading);
      await expect(page.locator(new URL(destination, baseURL).hash)).toBeInViewport();
    }
  } finally {
    await context.close();
  }
});

for (const [route, href, expected] of [
  ["deployment/", "/gh-cao/architecture/#control-plane-and-dashboard", "are deployed separately"],
  ["deployment-and-governance/", "/gh-cao/architecture/#repository-roles-and-records", "a target-side file cannot grant, narrow, or veto the decision"],
  ["execution-and-safety/", "/gh-cao/architecture/#execution-boundaries", "this reference supplies the exact handoff and invariants"],
  ["rollout-and-routing/", "/gh-cao/architecture/#authority-and-admission", "commit the assigned live authority and explicit target scope in the control repository"],
]) {
  test(`${route} links to its consolidated introduction`, async ({ page }) => {
    expect((await page.goto(route))?.ok()).toBe(true);
    const content = page.locator(".sl-markdown-content");
    await expect(content).toContainText(expected);
    await expect(content.locator(`a[href="${href}"]`)).toHaveCount(1);
    const response = await page.request.get(href);
    expect(response.ok()).toBe(true);
    expect(await response.text()).toContain(`id="${new URL(href, page.url()).hash.slice(1)}"`);
  });
}

test("legacy getting-started route redirects to interactive setup", async ({ page }) => {
  await page.goto("getting-started/");
  await expect(page).toHaveURL(/\/setup-quickstarts\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Set Up CAO");
});

for (const colorScheme of ["light", "dark"]) {
  for (const width of [320, 390, 768, 1152, 1153, 1280, 1920]) {
    test(`workflow ecosystem and campaign animation at ${width}px in ${colorScheme} mode`, async ({ page }, testInfo) => {
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.setViewportSize({ width, height: 900 });
      expect((await page.goto("./"))?.ok()).toBe(true);
      const ecosystem = page.locator(".workflow-ecosystem");
      const mobile = width <= 1152;
      await ecosystem.scrollIntoViewIfNeeded();
      await expect(ecosystem).toBeVisible();
      await expect(ecosystem.locator(".animation-toggle")).toBeHidden();
      await expect(ecosystem.locator(".ecosystem-legend")).toHaveCount(0);
      const successColor = colorScheme === "light" ? "rgb(45, 164, 78)" : "rgb(63, 185, 80)";
      const runningColor = colorScheme === "light" ? "rgb(191, 135, 0)" : "rgb(210, 153, 34)";
      const neutralColor = await ecosystem.locator(".ecosystem-node").first().evaluate((element) => getComputedStyle(element).color);
      const borderColor = await ecosystem.locator(".ecosystem-node").first().evaluate((element) => getComputedStyle(element).borderTopColor);
      const mutedColor = await ecosystem.locator(".management-label").first().evaluate((element) => getComputedStyle(element).color);
      for (const node of await ecosystem.locator(".ecosystem-node").all()) {
        await expect(node).toHaveCSS("border-top-color", borderColor);
        await expect(node.locator("header strong")).toHaveCSS("color", neutralColor);
        await expect(node.locator("header .octicon")).toHaveCSS("fill", neutralColor);
      }
      for (const continuation of await ecosystem.locator(".continuation-point").all()) {
        await expect(continuation).toHaveCSS("color", mutedColor);
      }
      for (const run of await ecosystem.locator(".workflow-run").all()) {
        await expect(run.locator(".progress-ring")).toHaveCSS("stroke", runningColor);
        await expect(run.locator(".status.complete")).toHaveCSS("fill", successColor);
      }
      for (const pulse of await ecosystem.locator(".route-pulse").all()) {
        await expect(pulse).toHaveCSS("fill", successColor);
      }
      await expect(page.locator(".control-plane")).toHaveCount(0);
      await expect(ecosystem.locator(".node-owner")).toHaveCount(0);
      await expect(ecosystem.getByText(/Enterprise automation team|Platform team|Engineering team|Organization team|Maintainers/)).toHaveCount(0);
      await expect(ecosystem.locator(".branch")).toHaveCount(0);
      for (const title of await ecosystem.locator(".workflow-run:visible strong").all()) {
        expect(await title.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      }
      await expect(ecosystem.locator(".organization-node:visible header strong")).toHaveText(mobile ? ["Org 1"] : ["Org 1", "Org 2", "Org N"]);
      await expect(ecosystem.locator(".repository-node:visible header strong")).toHaveText(mobile ? ["Repo 1", "Repo 2"] : ["Repo 1", "Repo 2", "Repo 3", "Repo N"]);
      await expect(ecosystem.locator(".enterprise-node .workflow-run strong")).toHaveText(["Optimization"]);
      await expect(ecosystem.getByText("Supply chain", { exact: true })).toHaveCount(0);
      await expect(ecosystem.locator(".enterprise-node .managed-workflows .management-label")).toHaveText("Centrally managed");
      await expect(ecosystem.locator(".organization-node .managed-workflows .management-label")).toHaveText([
        "Centrally managed", "Centrally managed", "Centrally managed",
      ]);
      for (const badge of await ecosystem.locator(".management-label:visible").all()) {
        expect(await badge.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      }
      if (mobile) {
        await expect(ecosystem.getByText("Org 2", { exact: true })).toBeHidden();
        await expect(ecosystem.getByText("Org N", { exact: true })).toBeHidden();
        await expect(ecosystem.getByText("Repo 3", { exact: true })).toBeHidden();
        await expect(ecosystem.getByText("Repo N", { exact: true })).toBeHidden();
        await expect(ecosystem.locator(".mobile-continuation")).toBeVisible();
        await expect(ecosystem.locator(".mobile-continuation")).toHaveText("... Org N / Repo N");
        await expect(ecosystem.locator(".continuation-point:visible")).toHaveCount(0);
        expect(await ecosystem.locator(".ecosystem-board").evaluate((element) => element.getBoundingClientRect().height)).toBeLessThanOrEqual(680);
      } else {
        await expect(ecosystem.getByText("Org 2", { exact: true })).toBeVisible();
        await expect(ecosystem.getByText("Org N", { exact: true })).toBeVisible();
        await expect(ecosystem.getByText("Repo N", { exact: true })).toBeVisible();
        await expect(ecosystem.getByRole("img", { name: "More organizations", exact: true })).toBeVisible();
        await expect(ecosystem.getByRole("img", { name: "More repositories", exact: true })).toBeVisible();
        await expect(ecosystem.locator(".mobile-continuation")).toBeHidden();
        expect(await ecosystem.locator(".ecosystem-board").evaluate((element) => element.getBoundingClientRect().height)).toBeLessThanOrEqual(500);
      }
      await expect(ecosystem.locator(".enterprise-node header")).toHaveCSS("font-size", "16px");
      expect(await ecosystem.locator(".enterprise-node .workflow-run").evaluate(element => (
        Number.parseFloat(getComputedStyle(element).height)
      ))).toBeCloseTo(44, 1);
      for (const repository of await ecosystem.locator(".repository-node:visible").all()) {
        const order = await repository.evaluate((element) => {
          const workflow = element.querySelector(".local-workflows");
          const outputs = element.querySelector(".workflow-outputs");
          return {
            follows: Boolean(workflow.compareDocumentPosition(outputs) & Node.DOCUMENT_POSITION_FOLLOWING),
            gap: outputs.getBoundingClientRect().top - workflow.getBoundingClientRect().bottom,
          };
        });
        expect(order.follows).toBe(true);
        expect(order.gap).toBeGreaterThanOrEqual(0);
        await expect(repository.locator(".local-workflows > .management-label")).toHaveText("Repo-managed \u00b7 monitored");
        await expect(repository.locator(".workflow-outputs .management-label")).toHaveCount(0);
        await expect(repository.locator(".workflow-outputs .safe-output-icon")).toHaveCount(3);
        const workflowName = await repository.locator(".local-workflows .workflow-run strong").textContent();
        const outputType = workflowName === "Daily team status" ? "discussion" : workflowName === "Test coverage" ? "pull request" : "issue";
        const localCaption = {
          "Daily team status": "Status", "Test coverage": "Tests", "Release checks": "Checks", "Project health": "Health",
        }[workflowName];
        await expect(repository.locator(".workflow-outputs small")).toHaveText([
          localCaption, "Optimize", /^(Triage|Readiness|Standardize)$/,
        ]);
        await expect(repository.getByRole("img", { name: `${workflowName}: Repository ${outputType} safe output`, exact: true })).toBeVisible();
        for (const output of await repository.locator(".safe-output-icon").all()) {
          await expect(output).toHaveCSS("color", successColor);
        }
        const labels = await repository.locator(".workflow-outputs small").evaluateAll((elements) => elements.map((element) => ({
          text: element.textContent,
          hasFullTitle: element.title.length >= element.textContent.length,
          height: element.getBoundingClientRect().height,
          lineHeight: Number.parseFloat(getComputedStyle(element).lineHeight),
          fontSize: Number.parseFloat(getComputedStyle(element).fontSize),
          fits: element.scrollWidth <= element.clientWidth && element.clientWidth <= element.parentElement.clientWidth,
        })));
        for (const label of labels) {
          expect(label.hasFullTitle).toBe(true);
          expect(label.height).toBeLessThanOrEqual(label.lineHeight + 1);
          expect(label.fontSize).toBeGreaterThanOrEqual(12);
          expect(label.fits).toBe(true);
        }
        await expect(repository.locator(".workflow-outputs .workflow-chip")).toHaveCount(0);
        await expect(repository.getByRole("img", { name: "Optimization: Enterprise open issue safe output", exact: true })).toBeVisible();
        await expect(repository.getByRole("img", { name: /: Organization open issue safe output$/ })).toBeVisible();
        expect(await repository.locator(".safe-output-icon svg").evaluateAll(
          (icons) => icons.map((icon) => [...icon.querySelectorAll("path")].map((path) => path.getAttribute("d"))),
        )).toEqual([safeOutputPaths[outputType], issueOpenedPaths, issueOpenedPaths]);
        await expect(repository.locator(".safe-output.local")).toBeVisible();
        const localOutputStyle = await repository.locator(".safe-output.local").evaluate((element) => ({
          link: Number.parseFloat(getComputedStyle(element, "::before").height),
          separation: getComputedStyle(element).borderRightWidth,
        }));
        expect(localOutputStyle.link).toBeCloseTo(12, 1);
        expect(localOutputStyle.separation).toBe("0px");
        expect(await repository.locator(".workflow-outputs").evaluate((element) => {
          const bounds = element.getBoundingClientRect();
          const outputs = [...element.querySelectorAll(".safe-output")].map((output) => output.getBoundingClientRect());
          const zoom = element.closest(".workflow-ecosystem").currentCSSZoom;
          return outputs.every((output, index) => (
            output.left >= bounds.left && output.right <= bounds.right + 0.1
            && (index === 0 || (output.left - outputs[index - 1].right) / zoom >= 2.9)
          ));
        })).toBe(true);
        await expect(repository.locator(".workflow-run")).toHaveCount(1);
        await expect(repository.locator(".local-workflows .workflow-run")).toHaveCount(1);
        expect(await repository.locator(".local-workflows .workflow-run").evaluate(element => (
          Number.parseFloat(getComputedStyle(element).height)
        ))).toBeCloseTo(36, 1);
        const outputRows = await repository.locator(".safe-output-icon").evaluateAll(
          (icons) => icons.map((icon) => icon.getBoundingClientRect().top),
        );
        expect(Math.max(...outputRows) - Math.min(...outputRows)).toBeLessThan(1);
        if (width >= 1280) await expect(repository).toHaveCSS("height", "160px");
        await expect(repository.locator(".local-workflows .status.complete")).toHaveCSS("opacity", "1");
      }
      expect(await page.locator("html").evaluate((element) => element.scrollWidth)).toBeLessThanOrEqual(width);
      for (const node of await ecosystem.locator(".ecosystem-node:visible").all()) {
        expect(await node.evaluate((element) => element.scrollHeight <= element.clientHeight)).toBe(true);
      }
      if (width <= 1152) {
        const alignment = await ecosystem.locator(".organization-node:visible, .repository-node:visible").evaluateAll((nodes) => nodes.map((node) => {
          const box = node.getBoundingClientRect();
          const header = node.querySelector("header").getBoundingClientRect();
          const link = getComputedStyle(node, "::before");
          const border = Number.parseFloat(getComputedStyle(node).borderTopWidth);
          return {
            header: (header.top + header.bottom) / 2 - box.top,
            connector: border + Number.parseFloat(link.top) + Number.parseFloat(link.borderTopWidth) / 2,
            pulses: [...node.querySelectorAll(".mobile-delivery")].map((pulse) => (
              border + Number.parseFloat(getComputedStyle(pulse).top) + 2.5
            )),
          };
        }));
        for (const node of alignment) {
          expect(Math.abs(node.connector - node.header)).toBeLessThan(0.1);
          for (const pulse of node.pulses) expect(Math.abs(pulse - node.header)).toBeLessThan(0.1);
        }
      } else {
        await expect(ecosystem.locator(".ecosystem-routes")).toBeVisible();
        await expect(ecosystem.locator(".ecosystem-routes")).toHaveCSS("animation-name", "none");
        const portDistances = await ecosystem.evaluate((element) => {
          const svg = element.querySelector(".ecosystem-routes");
          const segments = [...svg.querySelectorAll("path")].flatMap((path) => (
            path.getAttribute("d").match(/M[^M]+/g).map((subpath) => {
              const geometry = document.createElementNS("http://www.w3.org/2000/svg", "path");
              geometry.setAttribute("d", subpath);
              const start = geometry.getPointAtLength(0).matrixTransform(svg.getScreenCTM());
              const end = geometry.getPointAtLength(geometry.getTotalLength()).matrixTransform(svg.getScreenCTM());
              return { start, end };
            })
          ));
          return [...element.querySelectorAll("[data-node]")].flatMap((node) => {
            const box = node.getBoundingClientRect();
            const edges = node.classList.contains("enterprise-node") ? ["bottom"]
              : node.classList.contains("repository-node") ? ["top"] : ["top", "bottom"];
            return edges.map((edge) => {
              const x = (box.left + box.right) / 2;
              const y = box[edge];
              return Math.min(...segments.map(({ start, end }) => Math.hypot(
                x - Math.max(Math.min(start.x, end.x), Math.min(x, Math.max(start.x, end.x))),
                y - Math.max(Math.min(start.y, end.y), Math.min(y, Math.max(start.y, end.y))),
              )));
            });
          });
        });
        expect(portDistances).toHaveLength(11);
        for (const distance of portDistances) expect(distance).toBeLessThan(1);
        await page.emulateMedia({ reducedMotion: "no-preference" });
        await expect(ecosystem.locator(".route-pulse.optimization").first()).toHaveCSS("animation-duration", "14s");
        await page.emulateMedia({ reducedMotion: "reduce" });
      }
      const heroScreenshot = testInfo.outputPath(`ecosystem-${width}-${colorScheme}.png`);
      await page.locator(".dispatch-story").screenshot({ path: heroScreenshot });
      await testInfo.attach("Workflow ecosystem", { path: heroScreenshot, contentType: "image/png" });

      expect((await page.goto("catalog/"))?.ok()).toBe(true);
      await page.getByRole("heading", { name: "How campaigns run", exact: true }).scrollIntoViewIfNeeded();
      const flow = page.locator(".campaign-flow");
      await expect(flow.locator(".mobile-flow")).toBeVisible();
      await expect(flow.locator(".flow-layout")).toBeHidden();
      await expect(flow).toContainText("Their workflow definitions stay in the control repository");
      await expect(flow.locator(".mobile-campaign")).toHaveCount(2);
      expect(await page.locator("html").evaluate((element) => element.scrollWidth)).toBeLessThanOrEqual(width);
      const campaignScreenshot = testInfo.outputPath(`campaign-flow-${width}-${colorScheme}.png`);
      await flow.screenshot({ path: campaignScreenshot });
      await testInfo.attach("Campaign execution", { path: campaignScreenshot, contentType: "image/png" });
    });
  }
}

test("workflow ecosystem restores the unchanged desktop layout after a mobile resize", async ({ page }) => {
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("./");
    const ecosystem = page.locator(".workflow-ecosystem");
    const layout = () => ecosystem.evaluate((element) => {
      const bounds = element.getBoundingClientRect();
      return [...element.querySelectorAll("[data-node]")].map((node) => {
        const box = node.getBoundingClientRect();
        return { id: node.dataset.node, x: box.x - bounds.x, y: box.y - bounds.y, width: box.width, height: box.height };
      });
    });
    const desktop = await layout();
    await expect(ecosystem.locator(".ecosystem-node:visible")).toHaveCount(8);
    await page.setViewportSize({ width: 390, height: 900 });
    await expect(ecosystem.locator(".ecosystem-node:visible")).toHaveCount(4);
    await expect(ecosystem.locator(".mobile-continuation")).toBeVisible();
    await page.setViewportSize({ width: 1280, height: 900 });
    await expect(ecosystem.locator(".ecosystem-node:visible")).toHaveCount(8);
    await expect(ecosystem.locator(".mobile-continuation")).toBeHidden();
    expect(await layout()).toEqual(desktop);
  }
});

test("workflow ecosystem shared connector stems have the same paint weight as single branches", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  for (const colorScheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
    await page.goto("./");
    const coverage = await page.locator(".ecosystem-routes").evaluate(async (svg) => {
      const copy = svg.cloneNode(true);
      copy.querySelectorAll("circle").forEach((circle) => circle.remove());
      const { width, height } = svg.viewBox.baseVal;
      copy.setAttribute("width", width);
      copy.setAttribute("height", height);
      copy.style.cssText = `width:${width}px;height:${height}px`;
      const paths = [...svg.querySelectorAll("path")];
      copy.querySelectorAll("path").forEach((path, index) => {
        const style = getComputedStyle(paths[index]);
        path.style.cssText = `fill:none;stroke:${style.stroke};stroke-width:${style.strokeWidth};opacity:${style.opacity};vector-effect:non-scaling-stroke`;
      });
      const url = URL.createObjectURL(new Blob([new XMLSerializer().serializeToString(copy)], { type: "image/svg+xml" }));
      try {
        const image = new Image();
        image.src = url;
        await image.decode();
        const canvas = document.createElement("canvas");
        canvas.width = width;
        canvas.height = height;
        const context = canvas.getContext("2d");
        context.drawImage(image, 0, 0);
        const root = svg.parentElement.querySelector('[data-node="enterprise"]').getBoundingClientRect();
        const middle = svg.parentElement.querySelector('[data-node="org-2"]').getBoundingClientRect();
        const bounds = svg.getBoundingClientRect();
        const x = Math.round(((root.left + root.right) / 2 - bounds.left) / bounds.width * width);
        const top = (root.bottom - bounds.top) / bounds.height * height;
        const gap = (middle.top - root.bottom) / bounds.height * height;
        const sample = (y) => [...context.getImageData(x - 2, Math.round(y), 5, 1).data].filter((_, index) => index % 4 === 3);
        return { shared: sample(top + gap / 4), single: sample(top + gap * 3 / 4) };
      } finally {
        URL.revokeObjectURL(url);
      }
    });
    expect(coverage.shared.some((alpha) => alpha > 0)).toBe(true);
    expect(coverage.shared).toEqual(coverage.single);
    await expect(page.locator(".ecosystem-routes > path")).toHaveCount(2);
  }
});

for (const width of [390, 1280]) {
  test(`workflow ecosystem can pause and resume every animation with the keyboard at ${width}px`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "no-preference" });
    await page.setViewportSize({ width, height: 900 });
    await page.goto("./");
    const ecosystem = page.locator(".workflow-ecosystem");
    const pause = ecosystem.getByRole("button", { name: "Pause animation", exact: true });
    await pause.focus();
    await page.keyboard.press("Enter");
    const resume = ecosystem.getByRole("button", { name: "Resume animation", exact: true });
    await expect(resume).toBeFocused();
    const sample = () => ecosystem.evaluate(async (element) => {
      const animations = element.getAnimations({ subtree: true });
      await Promise.all(animations.map((animation) => animation.ready));
      const routes = element.querySelector(".ecosystem-routes");
      return {
        motionPaused: routes.animationsPaused(),
        motionTime: routes.getCurrentTime(),
        states: animations.map((animation) => animation.playState),
        times: animations.map((animation) => animation.currentTime),
      };
    });
    const paused = await sample();
    expect(paused.motionPaused).toBe(true);
    expect(paused.states.length).toBeGreaterThan(0);
    expect(paused.states.every((state) => state === "paused")).toBe(true);
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const stillPaused = await sample();
    expect(stillPaused.motionTime).toBe(paused.motionTime);
    expect(stillPaused.times).toEqual(paused.times);
    await page.keyboard.press("Space");
    await expect(pause).toBeFocused();
    const resumed = await sample();
    expect(resumed.motionPaused).toBe(false);
    expect(resumed.states.every((state) => state === "running")).toBe(true);
    await expect.poll(async () => (await sample()).motionTime).toBeGreaterThan(paused.motionTime);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await expect(pause).toBeHidden();
  });

  test(`workflow ecosystem animates runs before delivering safe outputs at ${width}px`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "no-preference" });
    await page.setViewportSize({ width, height: 900 });
    await page.goto("./");
    const ecosystem = page.locator(".workflow-ecosystem");
    await ecosystem.scrollIntoViewIfNeeded();
    const seek = (time) => ecosystem.evaluate((element, time) => {
      for (const animation of element.getAnimations({ subtree: true })) {
        animation.pause();
        animation.currentTime = time;
      }
      const routes = element.querySelector(".ecosystem-routes");
      routes.pauseAnimations();
      routes.setCurrentTime(time / 1000);
    }, time);
    const optimization = ecosystem.locator(".workflow-run.optimization");
    const successColor = await optimization.locator(".status.complete").evaluate((element) => getComputedStyle(element).fill);
    const mutedColor = await ecosystem.locator(".management-label").first().evaluate((element) => getComputedStyle(element).color);
    const repository = ecosystem.locator(".repository-node").first();
    const outputs = repository.locator(".safe-output-icon");
    const optimizationOutput = repository.locator(".safe-output.optimization .safe-output-icon");
    const organizationOutput = repository.locator(".safe-output.organization .safe-output-icon");
    const localOutput = repository.locator(".safe-output.local .safe-output-icon");
    await seek(500);
    await expect(optimization.locator(".status.queued")).toHaveCSS("opacity", "1");
    for (const output of await outputs.all()) {
      await expect(output).toHaveCSS("opacity", "0.35");
      await expect(output).toHaveCSS("color", mutedColor);
    }
    for (const label of await repository.locator(".safe-output small").all()) await expect(label).toHaveCSS("opacity", "1");
    await seek(1500);
    await expect(optimization.locator(".status.running")).toHaveCSS("opacity", "1");
    await expect(optimization.locator(".status.complete")).toHaveCSS("opacity", "0");
    await seek(4500);
    await expect(optimization.locator(".status.complete")).toHaveCSS("opacity", "1");
    await expect(optimizationOutput).toHaveCSS("opacity", "0.35");
    await expect(optimizationOutput).toHaveCSS("color", mutedColor);
    const delivery = width === 1280
      ? ecosystem.locator(".route-pulse.optimization").first()
      : ecosystem.locator(".enterprise-delivery .optimization").first();
    await expect(delivery).toHaveCSS("opacity", "1");
    await expect(delivery).toHaveCSS(width === 1280 ? "fill" : "background-color", successColor);
    await expect(ecosystem.locator(".workflow-run.local .status.running").first()).toHaveCSS("opacity", "1");
    await seek(5000);
    await expect(optimizationOutput).toHaveCSS("opacity", "1");
    await expect(optimizationOutput).toHaveCSS("color", successColor);
    await expect(organizationOutput).toHaveCSS("opacity", "0.35");
    await expect(organizationOutput).toHaveCSS("color", mutedColor);
    await seek(6000);
    await expect(organizationOutput).toHaveCSS("opacity", "1");
    await expect(organizationOutput).toHaveCSS("color", successColor);
    await expect(ecosystem.locator(".workflow-run.local .status.running").first()).toHaveCSS("opacity", "1");
    await seek(6500);
    await expect(ecosystem.locator(".workflow-run.local .status.complete").first()).toHaveCSS("opacity", "1");
    await expect(localOutput).toHaveCSS("opacity", "0.35");
    expect(await repository.locator(".safe-output.local").evaluate((element) => getComputedStyle(element, "::after").opacity)).toBe("1");
    for (const time of [7100, 10000, 13500]) {
      await seek(time);
      for (const output of await outputs.all()) {
        await expect(output).toHaveCSS("opacity", "1");
        await expect(output).toHaveCSS("color", successColor);
      }
      for (const status of await ecosystem.locator(".workflow-run:visible .status.complete").all()) await expect(status).toHaveCSS("opacity", "1");
    }
    await seek(14100);
    await expect(optimization.locator(".status.queued")).toHaveCSS("opacity", "1");
    for (const output of await outputs.all()) {
      await expect(output).toHaveCSS("opacity", "0.35");
      await expect(output).toHaveCSS("color", mutedColor);
    }

    await page.emulateMedia({ reducedMotion: "reduce" });
    for (const output of await outputs.all()) {
      await expect(output).toHaveCSS("opacity", "1");
      await expect(output).toHaveCSS("color", successColor);
    }
    for (const status of await ecosystem.locator(".workflow-run:visible .status.complete").all()) {
      await expect(status).toHaveCSS("opacity", "1");
    }
    await expect(delivery).toBeHidden();
  });
}

test("credential provisioning remains separate from its runtime authentication reference", async ({ page }) => {
  await page.goto("control-plane-authentication/");
  await expect(page).toHaveURL(/\/control-plane-authentication\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Control Plane Authentication Profiles");
  await page.locator(".sl-markdown-content").getByRole("link", { name: "Authentication", exact: true }).click();
  await expect(page).toHaveURL(/\/authentication\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Authentication");
});

test("removed setup wizard route redirects to the command quickstart", async ({ page }) => {
  expect((await page.goto("setup"))?.ok()).toBe(true);
  await expect(page).toHaveURL(/\/setup-quickstarts\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Set Up CAO");
});

for (const width of [320, 390, 768, 1024]) {
  test(`landing page exposes navigation and header controls at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    expect((await page.goto("./"))?.ok()).toBe(true);

    await expect(page.getByRole("button", { name: /Switch to (light|dark) theme/ })).toBeVisible();
    await expect(page.getByRole("link", { name: "GitHub repository" })).toBeVisible();

    const menu = page.locator(".cao-mobile-nav");
    await expect(menu.locator("summary")).toBeVisible();
    await expect(menu.getByRole("navigation", { name: "Global links menu" })).toBeHidden();
    await menu.locator("summary").click();
    await expect(menu).toHaveAttribute("open", "");
    await expect(menu.getByRole("navigation", { name: "Global links menu" })).toBeVisible();
    await expect(menu.getByRole("link")).toHaveText(["Docs"]);
    await expect(menu.getByRole("link", { name: "Docs", exact: true }))
      .toHaveAttribute("href", "/gh-cao/architecture-at-a-glance/");
    expect(await page.locator("html").evaluate((element) => element.scrollWidth))
      .toBeLessThanOrEqual(width);
  });
}

test("desktop documentation navigation links to the introduction", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  expect((await page.goto("setup-quickstarts/"))?.ok()).toBe(true);

  const navigation = page.getByRole("navigation", { name: "Global links", exact: true });
  await expect(navigation.getByRole("link")).toHaveText(["Docs"]);
  await navigation.getByRole("link", { name: "Docs", exact: true }).click();
  await expect(page).toHaveURL(/\/architecture-at-a-glance\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("What is Central Agentic Ops?");
});

for (const colorScheme of ["light", "dark"]) {
  test.describe(`${colorScheme} scheme`, () => {
    test.use({ colorScheme, viewport: { width: 320, height: 900 } });

    for (const path of paths) {
      test(`${path} does not overflow horizontally`, async ({ page }) => {
        expect((await page.goto(path))?.ok()).toBe(true);

        expect(await page.locator("html").evaluate((element) => element.scrollWidth)).toBeLessThanOrEqual(320);
      });
    }
  });
}

test("sidebar groups CAO-specific dashboard and intelligence documentation together", async ({ page }) => {
  expect((await page.goto("dashboard/"))?.ok()).toBe(true);

  const dashboardGroup = page.locator('nav[aria-label="Main"] summary').filter({ hasText: /^Dashboard$/ }).locator("..");
  await expect(dashboardGroup.locator(":scope > summary")).toHaveText("Dashboard");
  await expect(dashboardGroup.locator(":scope > ul > li > a")).toHaveText([
    "Overview",
    "Intelligence",
    "Runtime Health",
    "Portfolio Decisions",
    "Data Ingestion",
    "Data Model",
    "Dashboard Language",
  ]);
  await expect(dashboardGroup.locator("details")).toHaveCount(0);
});

for (const { name, viewport } of [
  { name: "desktop", viewport: { width: 1280, height: 900 } },
  { name: "wide desktop", viewport: { width: 1920, height: 1080 } },
  { name: "tablet", viewport: { width: 768, height: 1024 } },
  { name: "mobile", viewport: { width: 390, height: 844 } },
]) {
  for (const colorScheme of ["light", "dark"]) {
    test(`landing ${name} has compact branding and balanced spacing in ${colorScheme} mode`, async ({ page }, testInfo) => {
      await page.addInitScript(() => localStorage.removeItem("starlight-theme"));
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.setViewportSize(viewport);
      expect((await page.goto("./"))?.ok()).toBe(true);
      await page.evaluate(() => document.fonts.ready);

      const logo = page.locator(".site-title img:visible");
      const logoBounds = await logo.boundingBox();
      const title = page.locator(".site-title span");
      const titleBounds = await title.boundingBox();
      const titleFontSize = await title.evaluate((element) => parseFloat(getComputedStyle(element).fontSize));
      expect(logoBounds?.width).toBeLessThanOrEqual(titleFontSize);
      expect(logoBounds?.height).toBeLessThanOrEqual(titleBounds.height);
      expect(logoBounds?.width).toBeGreaterThanOrEqual(12);
      expect(await page.locator(".site-title span").evaluate(
        (element) => element.scrollWidth <= element.clientWidth,
      )).toBe(true);
      for (const action of await page.locator(".hero-action").all()) {
        expect((await action.boundingBox())?.height).toBeGreaterThanOrEqual(44);
      }

      const hero = await page.locator(".hierarchy-hero").boundingBox();
      const stories = await page.locator(".product-story").evaluateAll(
        (elements) => elements.map((element) => element.getBoundingClientRect().toJSON()),
      );
      expect(hero?.width).toBeLessThanOrEqual(1216);
      expect(Math.max(...stories.map((story) => story.width))
        - Math.min(...stories.map((story) => story.width))).toBeLessThanOrEqual(1);
      const eyebrow = await page.locator(".dispatch-story > .section-eyebrow").boundingBox();
      const heading = await page.locator("#dispatch-title").boundingBox();
      expect(heading.y - (eyebrow.y + eyebrow.height)).toBeLessThanOrEqual(16);
      if (name === "desktop" || name === "wide desktop") {
        const actions = await page.locator(".actions").boundingBox();
        expect(actions.y + actions.height).toBeLessThan(viewport.height);
      } else if (name === "mobile") {
        expect((await page.locator(".phone-preview").boundingBox())?.width).toBeLessThanOrEqual(240);
      }
      expect(await page.locator("html").evaluate((element) => element.scrollWidth))
        .toBeLessThanOrEqual(viewport.width);

      if (name === "desktop" || name === "mobile") {
        await page.locator(".product-shot").last().scrollIntoViewIfNeeded();
        await expect.poll(() => page.locator(".maintenance-image, .phone-image").evaluateAll(
          (images) => images.every((image) => image.complete && image.naturalWidth > 0),
        )).toBe(true);
        const rolloutStory = page.locator(".terminal-story");
        await rolloutStory.scrollIntoViewIfNeeded();
        await expect(rolloutStory).toContainText("The example adds the Dependabot campaign");
        await expect(rolloutStory).toContainText("a central orchestrator with one-target workers");
        await expect(rolloutStory.locator(".terminal")).toContainText("Install githubnext/gh-cao/dependabot");
        await expect(rolloutStory.locator(".terminal code").last()).toHaveText("Add the Dependabot campaign to CAO");
        await expect(rolloutStory.locator('[data-terminal-request="integration"]')).toContainText("Keep policy disabled, review-only");
        await expect(rolloutStory).toHaveCSS("opacity", "1");
        const rolloutScreenshot = testInfo.outputPath(`rollout-story-${name}-${colorScheme}.png`);
        await rolloutStory.screenshot({ path: rolloutScreenshot });
        await testInfo.attach(`Rollout story (${name}, ${colorScheme})`, {
          path: rolloutScreenshot, contentType: "image/png",
        });
        await page.evaluate(() => window.scrollTo(0, 0));
        const screenshot = testInfo.outputPath(`homepage-${name}-${colorScheme}.png`);
        await page.screenshot({ path: screenshot, fullPage: true });
        await testInfo.attach(`Homepage (${name}, ${colorScheme})`, { path: screenshot, contentType: "image/png" });
      }
    });

    test(`landing ${name} emphasizes overview and control in ${colorScheme} mode`, async ({ page }) => {
      await page.addInitScript(() => localStorage.removeItem("starlight-theme"));
      await page.emulateMedia({ colorScheme });
      await page.setViewportSize(viewport);
      expect((await page.goto("./"))?.ok()).toBe(true);

      await expect(page.getByRole("heading", { level: 1 })).toHaveText(
        "Monitor and govern GitHub Agentic Workflows at scale.",
      );
      const tagline = page.locator(".docs-hero-intro .tagline");
      await expect(tagline).toBeVisible();
      await expect(tagline).toHaveText("Keep track of all runs, costs, and outcomes of your GitHub Agentic Workflows across repositories. Set execution scope and rollout policy, refine workflows in review mode, and roll out with confidence.");
      await expect(page.getByRole("heading", { name: "Shared workflows. Clear ownership.", exact: true })).toBeAttached();
      await expect(page.getByRole("heading", { name: "EASY UPDATES, ALL IN ONE PLACE.", exact: true })).toBeAttached();
      await expect(page.locator(".runs-story, .value-story")).toHaveCount(0);
      await expect(page.getByRole("heading", { name: "Roll out with confidence.", exact: true })).toBeAttached();
      await expect(page.locator(".terminal-story .section-eyebrow")).toHaveText("Setup, authentication, and rollout");
      await expect(page.locator(".terminal-story .product-story-copy > p:not(.section-eyebrow)")).toHaveText(
        "Start with monitoring. Describe the outcome, target scope, and execution requirements; derive the simplest structure. The example adds the Dependabot campaign, pairing a central orchestrator with one-target workers. Keep repository scope, read/write credentials and review/live policy explicit. From outcome to execution.",
      );
      await expect(page.locator(".terminal-story").getByRole("link", { name: "From outcome to execution." }))
        .toHaveAttribute("href", "/gh-cao/operation-patterns/");
      await expect(page.locator('.product-story a[href^="https://github.github.com/gh-aw/practices/"]'))
        .toHaveCount(0);
      await expect(page.locator(".dispatch-story")).not.toContainText(/campaign/i);
      expect(await page.locator("html").evaluate((element) => element.scrollWidth))
        .toBeLessThanOrEqual(viewport.width);
    });

    test(`landing ${name} product story renders in ${colorScheme} mode`, async ({ page }) => {
      await page.addInitScript(() => localStorage.removeItem("starlight-theme"));
      await page.emulateMedia({ colorScheme });
      await page.setViewportSize(viewport);
      expect((await page.goto("./"))?.ok()).toBe(true);

      const pageCanvas = page.locator(".content-panel").first();
      const phone = page.locator(".phone-preview");
      await expect(page.locator("html")).toHaveAttribute("data-theme", colorScheme);
      await expect(phone).toBeVisible();
      await expect(page.locator(".terminal")).toBeAttached();
      await expect(page.locator(".terminal code")).toHaveText([
        "gh extension install githubnext/gh-cao",
        "gh cao setup",
        "Add the Dependabot campaign to CAO",
      ]);
      await expect(page.locator(".terminal a")).toHaveCount(0);
      await expect(page.locator('[data-terminal-output="install"]')).toHaveText("Installed extension githubnext/gh-cao");
      await expect(page.locator('[data-terminal-output="setup"]')).toHaveText(
        "Monitoring configured locally. No commit or push was performed.\nNext: gh cao doctor, then review, commit and push the installed files.\nOptional automation: https://githubnext.github.io/gh-cao/operation-patterns/",
      );
      await expect(page.locator(".terminal-label")).toHaveText([
        "In an existing control repository checkout",
        "Setup can separately offer reviewed publication; declining leaves files local.",
        "Example agent request — not a CLI command",
      ]);
      await expect(page.locator('[data-terminal-request="integration"]')).toHaveText(
        "Install githubnext/gh-cao/dependabot.\nUse its central orchestrator and one-target workers.\nKeep policy disabled, review-only, and scoped to one repo.\nReview the campaign files and policy. Do not enable or run.",
      );
      await expect(page.locator('[data-terminal-output="rollout"]')).toHaveCount(0);
      await expect(page.locator(".phone-image")).toHaveCount(1);
      await expect(page.locator(".product-shot-mobile")).toHaveCount(0);
      const maintenance = page.locator(".maintenance-story");
      await expect(maintenance.locator(".section-eyebrow")).toHaveText("One reviewed place");
      await expect(maintenance.locator("dt")).toHaveText(["01 gh-aw upgrades", "02 Workflow source updates"]);
      await expect(maintenance).toContainText("Keep workflows across all enrolled repositories up to date and secure.");
      await expect(maintenance).toContainText("Version pins keep installs reproducible");
      await expect(maintenance).toContainText("3-way merges preserve local customizations");
      await expect(maintenance).not.toContainText("campaign");
      await expect(maintenance.getByRole("link", { name: "gh aw upgrade", exact: true }))
        .toHaveAttribute("href", "https://github.github.com/gh-aw/setup/cli/#upgrade");
      await expect(maintenance.getByRole("link", { name: "gh aw update", exact: true }))
        .toHaveAttribute("href", "https://github.github.com/gh-aw/setup/cli/#update");
      await page.locator(".product-shot").last().scrollIntoViewIfNeeded();
      await expect.poll(() => page.locator(".phone-image, .maintenance-image").evaluateAll(
        (images) => images.every((image) => image.complete && image.naturalWidth > 0),
      )).toBe(true);
      const phoneSources = await page.locator(".phone-image").evaluateAll(
        (images) => images.map((image) => image.currentSrc),
      );
      expect(phoneSources.every((source) => colorScheme === "light"
        ? source.includes("-light.")
        : !source.includes("-light."))).toBe(true);
      const maintenanceSource = await maintenance.locator(`source[data-theme-source="${colorScheme}"]`).getAttribute("srcset");
      await expect.poll(() => maintenance.locator("img").evaluate(image => image.currentSrc)).toBe(maintenanceSource);
      const board = await maintenance.locator(".maintenance-board").boundingBox();
      const storyBounds = await maintenance.boundingBox();
      expect(board.x).toBeGreaterThanOrEqual(storyBounds.x);
      expect(board.x + board.width).toBeLessThanOrEqual(storyBounds.x + storyBounds.width);
      expect(board.y + board.height).toBeLessThanOrEqual(storyBounds.y + storyBounds.height);
      if (colorScheme === "dark") {
        await expect(pageCanvas).toHaveCSS("background-color", "rgb(0, 0, 0)");
        await expect(pageCanvas).toHaveCSS("background-image", "none");
        await expect(page.locator(".hierarchy-hero")).toHaveCSS("background-image", "none");
        for (const story of await page.locator(".product-story").all()) {
          await expect(story).toHaveCSS("background-color", "rgba(13, 17, 23, 0.88)");
          const background = await story.evaluate((element) => getComputedStyle(element).backgroundImage);
          expect(background.match(/radial-gradient\(/g)).toHaveLength(2);
          expect(background).toContain("rgba(63, 185, 80, 0.16)");
          expect(background).toContain("rgba(63, 185, 80, 0.1)");
          expect(await story.evaluate((element) => getComputedStyle(element, "::before").display))
            .toBe("none");
        }
      } else {
        expect(await pageCanvas.evaluate((element) => getComputedStyle(element).backgroundImage))
          .toContain("linear-gradient");
        for (const story of await page.locator(".product-story").all()) {
          await expect(story).toHaveCSS("background-color", "rgba(255, 255, 255, 0.84)");
          const background = await story.evaluate((element) => getComputedStyle(element).backgroundImage);
          expect(background.match(/radial-gradient\(/g)).toHaveLength(2);
          expect(background).toContain("at 0% 0%");
          expect(background).toContain("at 100% 100%");
          expect(background).toContain("0.12");
          expect(background).toContain("0.05");
        }
      }
      expect(await page.locator("html").evaluate((element) => element.scrollWidth))
        .toBeLessThanOrEqual(viewport.width);
    });
  }
}

test("landing product media follows a manual theme change", async ({ page }) => {
  await page.addInitScript(() => localStorage.removeItem("starlight-theme"));
  await page.emulateMedia({ colorScheme: "dark" });
  expect((await page.goto("./"))?.ok()).toBe(true);

  const maintenance = page.locator(".maintenance-image");
  await maintenance.scrollIntoViewIfNeeded();
  await expect(maintenance).toHaveJSProperty("currentSrc",
    await page.locator('.maintenance-board source[data-theme-source="dark"]').getAttribute("srcset"));
  await expect.poll(() => page.locator(".phone-image").evaluateAll(
    (images) => images.every((image) => !image.currentSrc.includes("-light.")),
  )).toBe(true);

  await page.getByRole("button", { name: "Switch to light theme" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(maintenance).toHaveJSProperty("currentSrc",
    await page.locator('.maintenance-board source[data-theme-source="light"]').getAttribute("srcset"));
  await expect.poll(() => page.locator(".phone-image").evaluateAll(
    (images) => images.every((image) => image.currentSrc.includes("-light.")),
  )).toBe(true);
});

const dashboardDiagramPages = [
  { name: "campaign execution", path: "architecture/" },
  { name: "data flow", path: "dashboard-data-ingestion/" },
];

for (const { name, path } of dashboardDiagramPages) {
  for (const colorScheme of ["light", "dark"]) {
    test(`${name} diagram loads its ${colorScheme} SVG in auto mode`, async ({ page }) => {
      await page.addInitScript(() => localStorage.removeItem("starlight-theme"));
      await page.emulateMedia({ colorScheme });
      expect((await page.goto(path))?.ok()).toBe(true);

      const diagram = page.locator(`.docs-theme-diagram-${colorScheme}`);
      const otherDiagram = page.locator(`.docs-theme-diagram-${colorScheme === "light" ? "dark" : "light"}`);
      await expect(page.locator("html")).toHaveAttribute("data-theme", colorScheme);
      await expect(diagram).toBeVisible();
      await expect(otherDiagram).toBeHidden();
      await expect(diagram).toHaveJSProperty("complete", true);
      expect(await diagram.evaluate((image) => image.naturalWidth)).toBeGreaterThan(0);
      expect(await diagram.evaluate((image) => image.naturalHeight)).toBeGreaterThan(0);
      expect(await diagram.evaluate((image) => image.currentSrc)).toContain(`-${colorScheme}.svg`);
    });
  }

  for (const theme of ["light", "dark"]) {
    test(`${name} diagram honors manual ${theme} mode`, async ({ page }) => {
      const oppositeTheme = theme === "light" ? "dark" : "light";
      await page.addInitScript(() => localStorage.removeItem("starlight-theme"));
      await page.emulateMedia({ colorScheme: oppositeTheme });
      expect((await page.goto(path))?.ok()).toBe(true);
      await page.getByRole("button", { name: `Switch to ${theme} theme` }).click();

      const diagram = page.locator(`.docs-theme-diagram-${theme}`);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await expect(diagram).toBeVisible();
      await expect(page.locator(`.docs-theme-diagram-${oppositeTheme}`)).toBeHidden();
      expect(await diagram.evaluate((image) => image.currentSrc)).toContain(`-${theme}.svg`);
    });
  }
}

test("long code remains in a keyboard-focusable local scroll region", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  await page.goto("configuration/");

  const code = page.locator(".sl-markdown-content pre").first();
  const dimensions = await code.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(dimensions.scrollWidth).toBeGreaterThan(dimensions.clientWidth);
  await expect(code).toHaveAttribute("tabindex", "0");
});
