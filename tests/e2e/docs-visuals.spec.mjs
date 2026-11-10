import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";

const architectureDiagramCount = [...readFileSync(
  new URL("../../docs/dashboard-data-architecture.md", import.meta.url), "utf8",
).matchAll(/```mermaid\n/g)].length;

const diagramPages = [
  "activity/",
  "dashboard-data-model/",
  "deployment-azure/",
  "intelligence/",
  "operational-value-how-to/",
];

for (const colorScheme of ["light", "dark"]) {
  for (const width of [390, 1280]) {
    test(`data architecture diagrams render accessibly at ${width}px in ${colorScheme}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      expect((await page.goto("dashboard-data-architecture/"))?.ok()).toBe(true);
      const diagrams = page.locator(".sl-markdown-content .mermaid svg");
      expect(architectureDiagramCount).toBeGreaterThanOrEqual(10);
      await expect(diagrams).toHaveCount(architectureDiagramCount);
      for (const diagram of await diagrams.all()) {
        await diagram.scrollIntoViewIfNeeded();
        await expect(diagram).toBeVisible();
        await expect(diagram.locator(":scope > title")).toHaveText(/\S/);
        await expect(diagram.locator(":scope > desc")).toHaveText(/\S/);
        await expect(diagram).toHaveAttribute("aria-labelledby", /\S/);
        await expect(diagram).toHaveAttribute("aria-describedby", /\S/);
        await expect(diagram).not.toContainText("Syntax error");
      }
      expect(await page.locator("html").evaluate(element => element.scrollWidth))
        .toBeLessThanOrEqual(width);
    });
  }

  for (const width of [320, 390, 1024, 1440]) {
    test(`workflow safety diagram renders at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.goto("./");
      const story = page.getByRole("region", { name: "Safety by design", exact: true });
      await story.scrollIntoViewIfNeeded();
      await expect(story).toBeVisible();
      await expect(page.locator(".agentic-workflows-story + .safety-story")).toHaveCount(1);
      await expect(story.locator(".safety-threat")).toHaveText([
        "Prompt Injection", "Permissions Abuse", "Data Exfiltration", "Malware Injection",
      ]);
      await expect(story.locator(".safety-label")).toHaveText(["Inputs", "Sandbox", "Safe Outputs"]);
      await expect(story.locator(".safety-agent")).toHaveText("Agent");
      const detection = story.locator(".safety-outputs .safety-detection");
      await expect(detection).toHaveText("Threat Detection");
      const detectionBounds = await detection.boundingBox();
      const outputsBounds = await story.locator(".safety-outputs ul").boundingBox();
      expect(detectionBounds.y + detectionBounds.height).toBeLessThanOrEqual(outputsBounds.y);
      await expect(story).toContainText("Only declared safe outputs may publish changes");
      await expect(story.getByRole("link", { name: "Security Architecture" }))
        .toHaveAttribute("href", "https://github.github.com/gh-aw/introduction/architecture/");
      const boxes = await story.locator(".safety-boundary").evaluateAll(nodes =>
        nodes.map(node => {
          const { x, y, width, height } = node.getBoundingClientRect();
          const fits = [...node.querySelectorAll("ul, .safety-agent, .safety-detection")].every(child => {
            const bounds = child.getBoundingClientRect();
            return bounds.x >= x && bounds.right <= x + width;
          });
          return { x, y, width, height, fits };
        }),
      );
      expect(boxes.every(box => box.fits)).toBe(true);
      if (width <= 960) {
        expect(boxes[1].y).toBeGreaterThan(boxes[0].y + boxes[0].height);
        expect(boxes[2].y).toBeGreaterThan(boxes[1].y + boxes[1].height);
      } else {
        expect(boxes[1].x).toBeGreaterThan(boxes[0].x + boxes[0].width);
        expect(boxes[2].x).toBeGreaterThan(boxes[1].x + boxes[1].width);
        expect(boxes[0].y).toBe(boxes[2].y);
      }
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      await page.evaluate(() => document.fonts.ready);
      const screenshot = testInfo.outputPath(`safety-${width}-${colorScheme}.png`);
      await story.screenshot({ path: screenshot });
      await testInfo.attach("Workflow safety diagram", { path: screenshot, contentType: "image/png" });
    });
  }
}

for (const colorScheme of ["light", "dark"]) {
  test(`retained Mermaid diagrams render with accessible descriptions in ${colorScheme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 900 });
    await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
    for (const path of diagramPages) {
      expect((await page.goto(path))?.ok(), path).toBe(true);
      const diagram = page.locator(".sl-markdown-content .mermaid svg");
      await expect(diagram, path).toHaveCount(1);
      await expect(diagram, path).toBeVisible();
      await expect(diagram.locator(":scope > title"), path).toHaveText(/\S/);
      await expect(diagram.locator(":scope > desc"), path).toHaveText(/\S/);
      await expect(diagram, path).toHaveAttribute("aria-labelledby", /\S/);
      await expect(diagram, path).toHaveAttribute("aria-describedby", /\S/);
      await expect(diagram, path).not.toContainText("Syntax error");
      expect(await page.locator("html").evaluate((element) => element.scrollWidth), path)
        .toBeLessThanOrEqual(390);
    }
  });

  for (const width of [390, 1280]) {
    test(`canonical diagrams enlarge for inspection at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      for (const path of ["architecture/", "dashboard-data-ingestion/"]) {
        expect((await page.goto(path))?.ok()).toBe(true);
        const image = page.locator(`.docs-theme-diagram-${colorScheme}`);
        await expect(image).toHaveAttribute("data-image-zoom-ready", "");
        await expect(image).toHaveJSProperty("complete", true);
        await image.scrollIntoViewIfNeeded();
        if (path === "dashboard-data-ingestion/") {
          const screenshot = testInfo.outputPath(`data-flow-${width}-${colorScheme}.png`);
          await page.screenshot({ path: screenshot });
          await testInfo.attach("Three-backend ingestion diagram", { path: screenshot, contentType: "image/png" });
        }
        const inlineWidth = await image.evaluate((element) => element.getBoundingClientRect().width);
        await image.focus();
        await page.keyboard.press("Enter");
        const dialog = page.getByRole("dialog", { name: "Expanded image", exact: true });
        const expanded = dialog.locator("img");
        await expect(dialog).toBeVisible();
        await expect(expanded).toHaveJSProperty("complete", true);
        const dimensions = await expanded.evaluate((element) => ({
          width: element.getBoundingClientRect().width,
          naturalWidth: element.naturalWidth,
        }));
        expect(dimensions.width).toBe(dimensions.naturalWidth);
        expect(dimensions.width).toBeGreaterThan(inlineWidth);
        const viewport = dialog.getByRole("region", { name: "Expanded image details" });
        await viewport.focus();
        if (width === 390) {
          await page.keyboard.press("ArrowRight");
          await expect.poll(() => viewport.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
        }
        await page.keyboard.press("Escape");
        await expect(dialog).not.toBeVisible();
        await expect(image).toBeFocused();
        expect(await page.locator("html").evaluate((element) => element.scrollWidth)).toBeLessThanOrEqual(width);
      }
    });

    test(`landing snapshots remain contextual and uncluttered at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      expect((await page.goto("./"))?.ok()).toBe(true);
      await expect(page.locator(".product-story")).toHaveCount(11);
      await expect(page.locator(".hero-callout, .evidence-storyboard")).toHaveCount(0);
      await expect(page.locator(".phone-preview figcaption")).toBeVisible();
      await expect(page.locator(".phone-preview figcaption")).toContainText("not verified improvements");
      await expect(page.locator(".maintenance-story figcaption")).toContainText("Example snapshot");
      await expect(page.locator(".maintenance-story figcaption")).toContainText("Unknown version evidence stays explicit");
      const value = page.locator(".decision-loop");
      await expect(value).toHaveCount(1);
      await expect(value.locator(".evidence-stages strong")).toHaveText(["Run", "Observe", "Review"]);
      await expect(value.locator(".decision-paths strong")).toHaveText(["Refine", "Expand", "Pause"]);
      await expect(value).toContainText("Human judgment.");
      await expect(value).toContainText("Reviewed policy.");
      await expect(value).toContainText("Successful runs alone do not demonstrate value");
      await expect(page.getByRole("link", { name: "Explore operational value" }))
        .toHaveAttribute("href", "/gh-cao/operational-value-how-to/");
      const valueScreenshot = testInfo.outputPath(`operational-value-${width}-${colorScheme}.png`);
      const valueStory = page.locator(".decision-story");
      await page.evaluate(() => document.fonts.ready);
      const valueHeight = await valueStory.evaluate((element) => element.getBoundingClientRect().height);
      // Keep the fixed header outside a tall storyboard's capture.
      await page.setViewportSize({ width, height: Math.max(900, Math.ceil(valueHeight) + 160) });
      await valueStory.screenshot({ path: valueScreenshot });
      await testInfo.attach("Operational value storyboard", { path: valueScreenshot, contentType: "image/png" });
      await page.setViewportSize({ width, height: 900 });
      const images = page.locator(".phone-image, .maintenance-image");
      await expect(images).toHaveCount(2);
      for (const image of await images.all()) {
        await image.scrollIntoViewIfNeeded();
        await expect.poll(() => image.evaluate((element) => element.complete && element.naturalWidth > 0)).toBe(true);
        await image.evaluate((element) => element.decode());
      }

      await page.evaluate(() => document.fonts.ready);
      await page.evaluate(() => window.scrollTo(0, 0));
      expect(await page.locator("html").evaluate((element) => element.scrollWidth)).toBeLessThanOrEqual(width);
      const screenshot = testInfo.outputPath(`docs-visual-cleanup-${width}-${colorScheme}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach("Simplified landing page", { path: screenshot, contentType: "image/png" });
    });
  }
}

for (const colorScheme of ["light", "dark"]) {
  for (const width of [320, 390, 1280]) {
    test(`factory observability hero renders at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      expect((await page.goto("./"))?.ok()).toBe(true);
      const story = page.getByRole("region", { name: "Understand your factory", exact: true });
      await story.scrollIntoViewIfNeeded();
      await expect(story.getByRole("heading", { name: "Understand your factory", exact: true })).toBeVisible();
      await expect(story.locator(".product-story-copy > p:not(.section-eyebrow)")).toHaveText([
        "CAO includes a dashboard with built-in views for workflow health, AIC cost, and operational value.",
        "Use OpenTelemetry to understand cost in AI Credits (AIC) and failures in real time. Pair traces, metrics, and logs with accepted outcomes to assess operational value.",
        "Analyze approved session traces in the optional agent session lake, powered by Kusto (Azure Data Explorer).",
        "All three are agent-ready: agents can mine approved evidence to investigate failures, explain AIC spend, and assess operational value.",
      ]);
      const dashboardLink = story.getByRole("link", { name: "built-in views", exact: true });
      await expect(dashboardLink).toHaveAttribute("href", "/gh-cao/dashboard/");
      expect((await page.request.get(await dashboardLink.getAttribute("href"))).ok()).toBe(true);
      await expect(story).not.toContainText(/experimental/i);
      await expect(story).toContainText("powered by Kusto (Azure Data Explorer)");
      const lakeLink = story.getByRole("link", { name: "agent session lake", exact: true });
      await expect(lakeLink).toHaveAttribute("href", "/gh-cao/dashboard-data-ingestion/#query-the-private-session-archive");
      const lakeResponse = await page.request.get(await lakeLink.getAttribute("href"));
      expect(lakeResponse.ok()).toBe(true);
      expect(await lakeResponse.text()).toContain('id="query-the-private-session-archive"');
      await expect(story).toContainText("All three are agent-ready");
      await expect(story).toContainText("agents can mine approved evidence");
      const agentLink = story.getByRole("link", { name: "agent-ready", exact: true });
      await expect(agentLink).toHaveAttribute("href", "/gh-cao/agent-analysis/");
      expect((await page.request.get(await agentLink.getAttribute("href"))).ok()).toBe(true);
      await expect(story).toContainText("Use OpenTelemetry");
      await expect(story).toContainText("cost in AI Credits (AIC) and failures in real time");
      await expect(story.locator(".metric dt")).toHaveText(["Run cost (AIC)", "Operational value", "Failures"]);
      await expect(story.locator(".metric").first()).toContainText("24.80");
      await expect(story).not.toContainText(/friction|\$\d/i);
      await expect(story.getByRole("img", { name: "Illustrative workflow activity over 24 hours" })).toBeVisible();
      await expect(story.locator(".trace-list li")).toHaveCount(3);
      await expect(story.locator("figcaption")).toContainText("not live telemetry");
      await expect(story.locator("figcaption")).toContainText("independently verified, accepted outcomes");
      const link = story.getByRole("link", { name: "Explore observability" });
      const href = await link.getAttribute("href");
      const response = await page.request.get(href);
      expect(response.ok()).toBe(true);
      expect(await response.text()).toContain('id="optional-observability"');
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      expect(await story.locator(".factory-dashboard").evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.evaluate(() => document.fonts.ready);
      const screenshot = testInfo.outputPath(`factory-${width}-${colorScheme}.png`);
      await story.screenshot({ path: screenshot });
      await testInfo.attach("Factory observability hero", { path: screenshot, contentType: "image/png" });
    });
  }
}
