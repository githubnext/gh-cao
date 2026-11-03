import { expect, test } from "@playwright/test";

for (const { name, viewport } of [
  { name: "desktop", viewport: { width: 1440, height: 900 } },
  { name: "mobile", viewport: { width: 390, height: 844 } },
]) {
  for (const { selector, heading } of [
    { selector: ".dispatch-story", heading: "Shared workflows. Clear ownership." },
    { selector: ".maintenance-story", heading: "Easy upkeep" },
    { selector: ".decision-story", heading: "Use evidence to decide what scales." },
  ]) {
    for (const colorScheme of ["light", "dark"]) {
      test(`${selector} resets after repeated scrolls on ${name} in ${colorScheme} mode`, async ({ page }, testInfo) => {
        await page.setViewportSize(viewport);
        await page.emulateMedia({ colorScheme, reducedMotion: "no-preference" });
        await page.goto("./");
        await page.evaluate(() => document.fonts.ready);
        const story = page.locator(selector);
        await expect(story).toHaveCSS("opacity", "0");

        for (let cycle = 0; cycle < 3; cycle++) {
          await story.scrollIntoViewIfNeeded();
          await expect(story).toHaveCSS("opacity", "1");
          await expect(page.getByRole("heading", { name: heading, exact: true })).toBeInViewport();
          if (cycle === 2 && colorScheme === "dark") {
            const path = testInfo.outputPath(`hero-${name}-revealed.png`);
            await page.screenshot({ path });
            await testInfo.attach(`${name} storyboard revealed`, { path, contentType: "image/png" });
          }

          await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
          await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
          await expect(story).toHaveCSS("opacity", "0");
          await expect(story).toHaveClass(/reveal-pending/);
          await expect(story).not.toHaveClass(/is-visible/);
        }

        if (colorScheme === "dark") {
          const path = testInfo.outputPath(`hero-${name}-reset.png`);
          await page.screenshot({ path });
          await testInfo.attach(`${name} hero reset at top`, { path, contentType: "image/png" });
        }
      });
    }
  }
}

test("landing stories remain visible with reduced motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("./");
  await expect(page.locator("[data-reveal]")).toHaveCount(11);
  await expect(page.locator(".maintenance-story")).toContainText("Dashboard maintenance");
  for (const story of await page.locator("[data-reveal]").all()) {
    await expect(story).toHaveCSS("opacity", "1");
    await expect(story).not.toHaveClass(/reveal-pending/);
  }
});

for (const colorScheme of ["light", "dark"]) {
  for (const width of [320, 390, 1440]) {
    test(`hero diagrams and campaign render at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.goto("./");
      const section = page.getByRole("region", { name: "Built on Agentic Workflows", exact: true });
      await section.scrollIntoViewIfNeeded();
      await expect(section).toBeVisible();
      await expect(page.locator(".product-story").last()).toHaveAttribute("aria-labelledby", "safety-title");
      await expect(section.getByRole("link", { name: "Explore GitHub Agentic Workflows" }))
        .toHaveAttribute("href", "https://github.github.com/gh-aw/");
      const example = section.getByRole("group", { name: "Illustrative example: from a prompt to a campaign to bounded repository work" });
      await expect(example).toContainText("name: Dependabot");
      await expect(example).toContainText(".github/workflows/dependabot-update-planner.md");
      await expect(example).toContainText("Campaign prepared for review");
      await expect(example).toContainText("disabled and review-only");
      const gateway = page.getByRole("region", { name: "Webhook Gateway", exact: true });
      const evolution = page.getByRole("region", { name: "Evolution at the Enterprise Scale", exact: true });
      await expect(evolution).toContainText("Hundreds of Copilot, Claude, and Codex agents");
      await expect(evolution.locator(".evolution-stages li")).toHaveCount(4);
      await expect(evolution).toContainText("Azure Blob Storage + Parquet");
      await expect(evolution).toContainText("Send query results to an optimizer agent");
      await expect(evolution).toContainText("Reviewed updates feed the next round of sessions");
      await expect(evolution.locator("[data-edge]")).toHaveCount(4);
      await expect(evolution.locator(".signal-port")).toHaveCount(8);
      await expect(evolution).toContainText("Block diagram / Closed loop");
      await expect(evolution.locator('[data-edge="optimizer-to-agents"]')).toContainText("Review gate");
      const blocks = await evolution.locator(".evolution-stages li").evaluateAll(nodes =>
        nodes.map(node => {
          const { x, y } = node.getBoundingClientRect();
          return { x, y };
        }),
      );
      expect(blocks[0].x).toBeLessThan(blocks[1].x);
      expect(blocks[0].y).toBeLessThan(blocks[3].y);
      expect(blocks[2].x).toBeGreaterThan(blocks[3].x);
      expect(blocks[1].y).toBeLessThan(blocks[2].y);
      await expect(page.getByRole("region", { name: "Admission Gate", exact: true })).toHaveCount(0);
      await expect(gateway).toContainText("reviewed enrollment");
      await expect(gateway).toContainText("Trigger approved workflows in enrolled repositories using repository_dispatch");
      const routing = gateway.getByRole("figure", { name: "Service webhook dispatch" });
      await expect(routing).toBeVisible();
      await expect(routing.locator(".source-services strong")).toHaveText(["Linear", "Jira", "Sentry", "Azure DevOps"]);
      await expect(routing.locator(".source-services svg")).toHaveCount(4);
      for (const logo of await routing.locator(".source-services svg").all()) {
        await expect(logo).toBeVisible();
        await expect(logo.locator("path")).toHaveAttribute("d", /^M.+/);
      }
      await expect(routing.locator(".destination-repository strong")).toHaveText("Enrolled repositories");
      await expect(routing.locator(".destination-repository")).toContainText("Approved agentic workflows");
      await expect(routing).not.toContainText("Control repository");
      await expect(routing).not.toContainText("acme/");
      await expect(routing).toContainText("supported event adapter");
      await expect(routing).toContainText("repository_dispatch");
      await expect(gateway.locator("pre")).toHaveCount(0);
      await expect(gateway.getByRole("link", { name: "Explore the Webhook Gateway" }))
        .toHaveAttribute("href", /#webhook-gateway-operations$/);
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      if (width !== 320) {
        for (const [name, story] of [["agentic-workflows", section], ["webhook-gateway", gateway], ["enterprise-evolution", evolution]]) {
          const path = testInfo.outputPath(`${name}-${width}-${colorScheme}.png`);
          await story.screenshot({ path });
          await testInfo.attach(name, { path, contentType: "image/png" });
        }
      }
    });
  }
}

for (const colorScheme of ["light", "dark"]) {
  for (const width of [320, 390, 1440]) {
    test(`Azure application map renders at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.goto("./");
      const backend = page.getByRole("region", { name: "Bring Your Own Backend", exact: true });
      await backend.scrollIntoViewIfNeeded();
      await expect(backend).toBeVisible();
      await expect(backend).toContainText("store and analyze agentic sessions");
      await expect(backend.getByRole("link", { name: "Explore the backend specifications" }))
        .toHaveAttribute("href", "https://github.com/githubnext/gh-cao/blob/main/server/spec/README.md");
      await expect(backend.getByRole("link", { name: "Explore deployment options" }))
        .toHaveAttribute("href", "/gh-cao/deployment/");
      const architecture = backend.getByRole("figure", { name: "Specification to Azure resource map" });
      await expect(architecture.locator(".backend-node strong")).toHaveText(["CAO server", "Dashboard", "PostgreSQL", "Blob Storage", "Data Explorer"]);
      await expect(architecture.locator("[data-edge]")).toHaveCount(7);
      await expect(architecture.locator(".service-icon svg")).toHaveCount(8);
      await expect(architecture.locator(".dashboard-node")).toContainText("Served by CAO");
      await expect(architecture.locator('[data-edge="server-to-dashboard"]')).toHaveCount(1);
      await expect(architecture.locator('[data-edge="postgres-to-dashboard"]')).toHaveCount(0);
      const readWriteLabel = await architecture.locator(".database-label").boundingBox();
      const dashboard = await architecture.locator(".dashboard-node").boundingBox();
      expect(readWriteLabel.y).toBeGreaterThan(dashboard.y + dashboard.height);
      const eventFlow = architecture.getByRole("group", { name: "Webhook ingestion and opt-in Actions dispatch" });
      await expect(eventFlow.locator(".event-step strong")).toHaveText(["Webhooks", "Gateway", "Actions"]);
      await expect(eventFlow).toContainText("Reviewed enrollment");
      await expect(eventFlow).toContainText("repository_dispatch");
      await expect(architecture.locator(".evidence-handoff")).toContainText("Collected run evidence");
      await expect(architecture.locator(".evidence-handoff")).toContainText("Webhook events");
      await expect(architecture.locator('.evidence-handoff [data-edge="webhooks-to-server"]')).toHaveCount(1);
      await expect(architecture.locator('[data-edge="webhooks-to-gateway"]')).toHaveCount(0);
      await expect(architecture.locator(".specification")).toContainText("CAO TypeSpec");
      await expect(architecture.locator(".application-map")).toContainText("Azure resource map");
      await expect(architecture.locator(".backend-node small")).toHaveText(["Optional", "Optional"]);
      await expect(architecture.locator(".network-legend")).toContainText("Optional archive + analytics");
      const positions = await architecture.locator(".backend-node:not(.dashboard-node) .service-icon").evaluateAll(nodes =>
        nodes.map(node => {
          const { x, y } = node.getBoundingClientRect();
          return { x, y };
        }),
      );
      expect(positions[0].x).toBeGreaterThan(positions[1].x);
      expect(positions[0].y).toBeLessThan(positions[1].y);
      expect(positions[2].x).toBeGreaterThan(positions[1].x);
      expect(positions[2].y).toBe(positions[1].y);
      expect(positions[3].x).toBe(positions[0].x);
      expect(positions[3].y).toBeGreaterThan(positions[2].y);
      for (const icon of await architecture.locator(".service-icon").all()) {
        await expect(icon).toHaveCSS("border-radius", "50%");
      }
      const legendTop = await architecture.locator(".network-legend").evaluate(element => element.getBoundingClientRect().top);
      for (const node of await architecture.locator(".backend-node").all()) {
        expect(await node.evaluate(element => element.getBoundingClientRect().bottom)).toBeLessThan(legendTop);
      }
      await expect(architecture).toContainText("independently opt-in");
      for (const node of await architecture.locator(".backend-node").all()) {
        await expect(node).toBeVisible();
        const overflowingLabels = await node.evaluate(element =>
          [...element.querySelectorAll("strong, small, span")].filter(label =>
            label.scrollWidth > label.clientWidth || label.getBoundingClientRect().width > element.getBoundingClientRect().width,
          ).map(label => label.textContent),
        );
        expect(overflowingLabels).toEqual([]);
      }
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      if (width !== 320) {
        const path = testInfo.outputPath(`bring-your-own-backend-${width}-${colorScheme}.png`);
        await architecture.screenshot({ path, style: "header.header { position: absolute !important; }" });
        await testInfo.attach("Bring Your Own Backend", { path, contentType: "image/png" });
      }
    });
  }
}

test("landing stories remain visible without JavaScript", async ({ browser, baseURL }) => {
  const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    await page.goto("./");
    await expect(page.locator("[data-reveal]")).toHaveCount(11);
    await expect(page.locator(".maintenance-story")).toContainText("Dashboard maintenance");
    for (const story of await page.locator("[data-reveal]").all()) {
      await expect(story).toHaveCSS("opacity", "1");
    }
    for (const name of ["Webhook Gateway"]) {
      const section = page.getByRole("region", { name, exact: true });
      await expect(section).toBeVisible();
      await expect(section.getByRole("figure")).toBeVisible();
      await expect(section.locator(".diagram-node")).toHaveCount(6);
    }
    await expect(page.locator(".agentic-workflows-story .workflow-example-card")).toHaveCount(3);
    await expect(page.locator(".agentic-workflows-story pre code")).not.toBeEmpty();
    await expect(page.locator(".decision-loop .evidence-stages li")).toHaveCount(3);
    await expect(page.locator(".decision-loop")).toContainText("Reviewed policy.");
    await expect(page.locator(".decision-loop .decision-paths strong")).toHaveText(["Refine", "Expand", "Pause"]);
    await expect(page.locator(".enterprise-evolution .evolution-stages li")).toHaveCount(4);
    await expect(page.locator(".enterprise-evolution")).toContainText("Architecture pattern.");
    await expect(page.getByRole("region", { name: "Bring Your Own Backend", exact: true }).getByRole("figure")).toBeVisible();
  } finally {
    await context.close();
  }
});
