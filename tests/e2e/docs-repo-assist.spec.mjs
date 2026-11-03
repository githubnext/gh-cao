import { expect, test } from "@playwright/test";

for (const colorScheme of ["light", "dark"]) {
  for (const width of [320, 390, 1440]) {
    test(`reconstructed Repo Assist chart at ${width}px in ${colorScheme}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.goto("./");
      const section = page.getByRole("region", { name: "Repos that clean themselves", exact: true });
      await section.scrollIntoViewIfNeeded();
      await expect(section).toBeVisible();
      await expect(section.getByRole("link", { name: "Explore Repo Assist" }))
        .toHaveAttribute("href", "/gh-cao/repo-assist-quickstart/");
      await expect(section.getByRole("img", { name: "Open issues before and after Repo Assist adoption", exact: true })).toBeVisible();
      await expect(section.locator(".repository-legend li")).toHaveCount(14);
      await expect(section.locator("img, image, canvas")).toHaveCount(0);
      await expect(section.locator(".repository-traces > path")).toHaveCount(14);
      await expect(section.locator(".repository-traces > .dashed")).toHaveCount(4);
      await expect(section.locator(".motion-trails > path")).toHaveCount(3);
      await expect(section.locator(".flying-agents > .flying-agent")).toHaveCount(3);
      await expect(section.locator(".adoption-marker")).toContainText("Adoption day");
      await expect(section.locator("figcaption")).toContainText("Traces are approximate");
      await expect(section.locator("figcaption")).toContainText("not live CAO data or evidence of causation");
      expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
      const chart = section.getByRole("region", { name: /Issue burndown chart/ });
      await chart.focus();
      await expect(chart).toBeFocused();
      if (width < 400) {
        expect(await chart.evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true);
        await page.keyboard.press("ArrowRight");
        await expect.poll(() => chart.evaluate(element => element.scrollLeft)).toBeGreaterThan(0);
      }
      if (width !== 320) {
        const path = testInfo.outputPath(`repo-assist-${width}-${colorScheme}.png`);
        await section.screenshot({ path });
        await testInfo.attach("Repo Assist reconstruction", { path, contentType: "image/png" });
      }
    });
  }
  for (const viewport of [{ width: 800, height: 600 }, { width: 1280, height: 480 }]) {
    test(`Repo Assist presentation fits ${viewport.width}x${viewport.height} in ${colorScheme}`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
      await page.goto("./");
      const index = await page.locator("[data-slideshow-slides] > [data-slideshow-slide]").evaluateAll(
        slides => slides.findIndex(slide => slide.classList.contains("repo-assist-story")),
      );
      expect(index).toBeGreaterThan(0);
      await page.getByRole("button", { name: "Start slideshow", exact: true }).click();
      const dialog = page.getByRole("dialog", { name: "Central Agentic Ops presentation" });
      for (let step = 0; step < index; step++) {
        await page.getByRole("button", { name: "Next slide", exact: true }).click();
      }
      await expect(dialog).toHaveAttribute("data-slide-index", String(index));
      const story = dialog.locator(".repo-assist-story");
      await expect(story).toBeVisible();
      await expect.poll(async () => story.evaluate(element => {
        const bounds = element.getBoundingClientRect();
        return bounds.left >= -1 && bounds.top >= -1
          && bounds.right <= window.innerWidth + 1 && bounds.bottom <= window.innerHeight + 1;
      })).toBe(true);
      expect(await story.locator(".chart-scroll").evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    });
  }
}

test("Repo Assist reconstruction is available without JavaScript", async ({ browser, baseURL }) => {
  const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    await page.goto("./");
    const section = page.getByRole("region", { name: "Repos that clean themselves", exact: true });
    await expect(section).toBeVisible();
    await expect(section.locator(".repository-traces > path")).toHaveCount(14);
    await expect(section.locator("figcaption")).toContainText("original measurements were not provided");
  } finally {
    await context.close();
  }
});
