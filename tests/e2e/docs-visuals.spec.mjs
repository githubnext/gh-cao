import { expect, test } from "@playwright/test";

const diagramPages = [
  "activity/",
  "dashboard-data-model/",
  "deployment-azure/",
  "deployment-upstash/",
  "intelligence/",
  "operational-value-how-to/",
];

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
      await expect(page.locator(".product-story")).toHaveCount(6);
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
