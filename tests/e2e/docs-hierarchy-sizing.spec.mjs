import { expect, test } from "@playwright/test";

for (const width of [320, 390, 800, 1152, 1153, 1280, 1440, 1920]) {
  test(`landing sections keep bounded content and alternating widescreen layouts at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("./");
    await page.evaluate(() => document.fonts.ready);
    const sections = page.locator(".hierarchy-hero, .product-story");
    await expect(sections).toHaveCount(11);
    for (const [index, section] of (await sections.all()).entries()) {
      await section.scrollIntoViewIfNeeded();
      const layout = await section.evaluate(element => {
        const bounds = element.getBoundingClientRect();
        const copy = element.querySelector(".docs-hero-intro, .product-story-copy");
        const visual = element.lastElementChild;
        const box = child => {
          const rect = child.getBoundingClientRect();
          return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom };
        };
        return {
          width: bounds.width,
          height: bounds.height,
          copy: box(copy),
          visual: box(visual),
          contained: [...element.querySelectorAll("h1, h2, p, a, dt, dd, figcaption"), visual]
            .filter(child => child.checkVisibility() && !child.closest(".visually-hidden"))
            .every(child => {
              const rect = child.getBoundingClientRect();
              return rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1
                && rect.top >= bounds.top - 1 && rect.bottom <= bounds.bottom + 1;
            }),
        };
      });
      expect(layout.contained, `section ${index} content fits`).toBe(true);
      if (width >= 1152) {
        expect(layout.width / layout.height, `section ${index} aspect ratio`).toBeCloseTo(16 / 9, 2);
        const left = index % 2 === 0 ? layout.copy : layout.visual;
        const right = index % 2 === 0 ? layout.visual : layout.copy;
        expect(left.right, `section ${index} alternates copy placement`).toBeLessThan(right.left);
      } else {
        expect(layout.copy.bottom, `section ${index} stacks copy before its visual`).toBeLessThanOrEqual(layout.visual.top);
      }
    }
    expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
    if (width === 390 || width === 1440) {
      const path = testInfo.outputPath(`landing-sections-${width}.png`);
      await page.screenshot({ path, fullPage: true });
      await testInfo.attach("Responsive landing sections", { path, contentType: "image/png" });
    }
  });
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 800, height: 600 }, { width: 1280, height: 480 }]) {
  test(`alternating sections remain bounded in the slideshow at ${viewport.width}x${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("./");
    await page.getByRole("button", { name: "Start slideshow", exact: true }).click();
    const dialog = page.locator("#landing-slideshow");
    for (let index = 0; index < 12; index++) {
      await expect(dialog).toHaveAttribute("data-slide-index", String(index));
      await expect.poll(() => dialog.locator("[data-slideshow-slide]:visible").evaluate(section => {
        const bounds = section.getBoundingClientRect();
        return bounds.width / bounds.height;
      })).toBeCloseTo(16 / 9, 2);
      await expect.poll(() => dialog.locator("[data-slideshow-slide]:visible").evaluate(section => {
        const bounds = section.getBoundingClientRect();
        return bounds.left >= -1 && bounds.right <= innerWidth + 1
          && bounds.top >= -1 && bounds.bottom <= innerHeight + 1
          && [...section.querySelectorAll("h1, h2, p, figcaption")]
            .filter(child => child.checkVisibility() && !child.closest(".visually-hidden"))
            .every(child => {
              const rect = child.getBoundingClientRect();
              return rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1
                && rect.top >= bounds.top - 1 && rect.bottom <= bounds.bottom + 1;
            });
      })).toBe(true);
      if (index < 11) await page.keyboard.press("ArrowRight");
    }
  });
}
