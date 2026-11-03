import { expect, test } from "@playwright/test";

for (const colorScheme of ["light", "dark"]) {
  for (const width of [390, 1280]) {
    test(`landing workflow reads as highlighted Markdown at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.setViewportSize({ width, height: 1000 });
      expect((await page.goto("./"))?.ok()).toBe(true);
      await page.evaluate(() => document.fonts.ready);
      const story = page.locator(".agentic-workflows-story");
      await story.scrollIntoViewIfNeeded();
      await expect(story.locator(".workflow-file-name")).toHaveText(".github/workflows/dependabot.md");
      const code = story.locator("pre.astro-code code");
      await expect(code).toContainText("# Tackle the Dependabot backlog");
      await expect(code).toContainText("security-critical repos");
      await expect(code).toHaveCSS("font-family", /"Mona Sans Mono"/);
      await expect(story.locator(".workflow-file-name")).toHaveCSS("font-family", /"Mona Sans Mono"/);
      expect(await code.locator("span[style]").evaluateAll(spans =>
        new Set(spans.map(span => getComputedStyle(span).color)).size)).toBeGreaterThan(1);
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      const screenshot = testInfo.outputPath(`workflow-code-${width}-${colorScheme}.png`);
      await story.screenshot({ path: screenshot });
      await testInfo.attach("Markdown workflow example", { path: screenshot, contentType: "image/png" });
    });
  }
}

for (const colorScheme of ["light", "dark"]) {
  for (const width of [320, 390, 800, 1280, 1920]) {
    test(`docs reading rhythm and text-only navigation at ${width}px in ${colorScheme}`, async ({ page }) => {
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.setViewportSize({ width, height: 1000 });
      expect((await page.goto("architecture-at-a-glance/"))?.ok()).toBe(true);
      await page.evaluate(() => document.fonts.ready);
      await expect(page.locator("html")).toHaveAttribute("data-theme", colorScheme);

      const title = page.locator("h1#_top");
      const prose = page.locator(".sl-markdown-content > p").first();
      await expect(title).toHaveCSS("font-family", /"Mona Sans"/);
      expect(await page.evaluate(() => document.fonts.check('16px "Mona Sans"'))).toBe(true);
      await expect(page.locator(".sl-markdown-content h2").first()).toHaveCSS("margin-top", "48px");
      const titleBounds = await title.boundingBox();
      const proseBounds = await prose.boundingBox();
      expect(titleBounds.x).toBeCloseTo(proseBounds.x, 0);
      expect(proseBounds.width).toBeLessThanOrEqual(620);
      expect(proseBounds.y - (titleBounds.y + titleBounds.height)).toBeCloseTo(28, 0);
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);

      if (width < 800) {
        await page.getByRole("button", { name: "Menu", exact: true }).click();
      }
      const current = page.locator('.sidebar .top-level a[aria-current="page"]');
      await expect(current).toBeVisible();
      await expect(current).toHaveCSS("font-size", "14px");
      await expect(current).toHaveCSS("line-height", "20.3px");
      await expect(current).toHaveCSS("font-weight", "600");
      await expect(current).toHaveCSS("text-decoration-line", "none");
      await expect(current).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
      await current.hover();
      await expect(current).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
      await expect(current).toHaveCSS("text-decoration-line", "none");
      await page.keyboard.press("Tab");
      await current.focus();
      await expect(current).toHaveCSS("outline-style", "solid");
      await expect(current).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
      await expect(current).toHaveCSS("text-decoration-line", "none");
      if (width < 800) {
        expect((await current.boundingBox()).height).toBeGreaterThanOrEqual(40);
      }

      const summary = page.locator(".sidebar summary").filter({ hasText: /^Introduction$/ });
      await expect(summary).toHaveCSS("font-size", "14px");
      await summary.click();
      await expect(current).toBeHidden();
      await summary.click();
      await expect(current).toBeVisible();
      if (width < 800) {
        await page.getByRole("button", { name: "Menu", exact: true }).click();
      }

      const toc = width >= 1152 ? page.locator("starlight-toc") : page.locator("mobile-starlight-toc");
      if (width < 1152) await toc.locator("summary").click();
      const section = toc.locator('a:not([href="#_top"])').first();
      await expect(section).toHaveCSS("font-size", "14px");
      await section.click();
      await expect(page).toHaveURL(/#.+/);
      const activeSection = toc.locator('a[aria-current="true"]');
      await expect(activeSection).toHaveCSS("font-weight", "600");
      await expect(activeSection).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
      await expect(activeSection).toHaveCSS("text-decoration-line", "none");

      expect((await page.goto("dashboard/"))?.ok()).toBe(true);
      await expect(page.locator(".sl-markdown-content h3").first()).toHaveCSS("margin-top", "32px");
    });
  }
}
