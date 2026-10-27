import { expect, test } from "@playwright/test";

for (const { name, viewport } of [
  { name: "desktop", viewport: { width: 1440, height: 900 } },
  { name: "mobile", viewport: { width: 390, height: 844 } },
]) {
  for (const { selector, heading } of [
    { selector: ".dispatch-story", heading: "Shared workflows. Clear ownership." },
    { selector: ".maintenance-story", heading: "Distributed reach. Central upkeep." },
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
  await expect(page.locator("[data-reveal]")).toHaveCount(6);
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
      await expect(page.locator(".product-story").last()).toHaveAttribute("aria-labelledby", "agentic-workflows-title");
      await expect(section.getByRole("link", { name: "Explore GitHub Agentic Workflows" }))
        .toHaveAttribute("href", "https://github.github.com/gh-aw/");
      const example = section.getByRole("group", { name: "Illustrative example: from a prompt to a campaign to bounded repository work" });
      await expect(example).toContainText("name: Dependabot");
      await expect(example).toContainText(".github/workflows/dependabot-update-planner.md");
      await expect(example).toContainText("Campaign prepared for review");
      await expect(example).toContainText("disabled and review-only");
      const gateway = page.getByRole("region", { name: "Webhook Gateway", exact: true });
      await expect(page.getByRole("region", { name: "Admission Gate", exact: true })).toHaveCount(0);
      await expect(gateway).toContainText("reviewed enrollment");
      await expect(gateway).toContainText("Dispatch events across repositories");
      const routing = gateway.getByRole("figure", { name: "Cross-repository webhook dispatch" });
      await expect(routing).toBeVisible();
      await expect(routing.locator(".source-repositories strong")).toHaveText(["acme/service-a", "acme/service-b"]);
      await expect(routing.locator(".destination-repository strong")).toHaveText("acme/control");
      await expect(routing).toContainText("Cross-repo dispatch");
      await expect(gateway.locator("pre")).toHaveCount(0);
      await expect(gateway.getByRole("link", { name: "Explore the Webhook Gateway" }))
        .toHaveAttribute("href", /#webhook-gateway-operations$/);
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      if (width !== 320) {
        for (const [name, story] of [["agentic-workflows", section], ["webhook-gateway", gateway]]) {
          const path = testInfo.outputPath(`${name}-${width}-${colorScheme}.png`);
          await story.screenshot({ path });
          await testInfo.attach(name, { path, contentType: "image/png" });
        }
      }
    });
  }
}

test("landing stories remain visible without JavaScript", async ({ browser, baseURL }) => {
  const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    await page.goto("./");
    await expect(page.locator("[data-reveal]")).toHaveCount(6);
    await expect(page.locator(".maintenance-story")).toContainText("Dashboard maintenance");
    for (const story of await page.locator("[data-reveal]").all()) {
      await expect(story).toHaveCSS("opacity", "1");
    }
    for (const name of ["Webhook Gateway"]) {
      const section = page.getByRole("region", { name, exact: true });
      await expect(section).toBeVisible();
      await expect(section.getByRole("figure")).toBeVisible();
      await expect(section.locator(".diagram-node")).toHaveCount(4);
    }
    await expect(page.locator(".agentic-workflows-story .workflow-example-card")).toHaveCount(3);
    await expect(page.locator(".agentic-workflows-story pre code")).not.toBeEmpty();
    await expect(page.locator(".decision-loop .evidence-stages li")).toHaveCount(3);
    await expect(page.locator(".decision-loop")).toContainText("Reviewed policy.");
    await expect(page.locator(".decision-loop .decision-paths strong")).toHaveText(["Refine", "Expand", "Pause"]);
  } finally {
    await context.close();
  }
});
