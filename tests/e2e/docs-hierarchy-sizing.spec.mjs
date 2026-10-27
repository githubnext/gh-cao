import { expect, test } from "@playwright/test";

for (const width of [320, 390, 800, 1280, 1440, 1920]) {
  test(`ownership copy wraps responsively at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("./");
    await page.evaluate(() => document.fonts.ready);
    const copy = page.locator(".dispatch-story > p:not(.section-eyebrow)");
    await expect(copy).toHaveText(
      "Enrolled repositories benefit from enterprise-, organization-, and repository-owned workflows working side by side. Shared workflows reach each repository without being copied into it, while ownership and scope remain explicit.",
    );
    const layout = await copy.evaluate(paragraph => {
      const range = document.createRange();
      range.selectNodeContents(paragraph);
      const bounds = paragraph.getBoundingClientRect();
      const lines = [...range.getClientRects()].filter(rect => rect.width > 0 && rect.height > 0);
      return {
        lineCount: new Set(lines.map(rect => rect.top)).size,
        contained: lines.every(rect => rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1),
        left: bounds.left,
        right: bounds.right,
      };
    });
    if (width >= 1280) {
      expect(layout.lineCount).toBe(2);
    } else if (width <= 390) {
      expect(layout.lineCount).toBeGreaterThan(2);
    }
    expect(layout.contained).toBe(true);
    expect(layout.left).toBeGreaterThanOrEqual(0);
    expect(layout.right).toBeLessThanOrEqual(width);
    expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
  });
}

async function measureHierarchy(page) {
  return page.locator(".dispatch-story").evaluate(section => {
    const figure = section.querySelector(".dispatch-illustration");
    const ecosystem = section.querySelector(".workflow-ecosystem");
    const board = section.querySelector(".ecosystem-board");
    const heading = section.querySelector("h2");
    const box = element => {
      const bounds = element.getBoundingClientRect();
      return { x: bounds.x, right: bounds.right, bottom: bounds.bottom, width: bounds.width, height: bounds.height };
    };
    return {
      section: box(section), figure: box(figure), board: box(board), heading: box(heading),
      card: box(section.querySelector(".enterprise-node")),
      zoom: Number(getComputedStyle(ecosystem).zoom),
      padding: getComputedStyle(section).padding,
    };
  });
}

for (const width of [390, 1152, 1153, 1280, 1440, 1920]) {
  test(`hierarchy occupies a proportionally smaller desktop footprint at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("./");
    await page.locator(".dispatch-story").scrollIntoViewIfNeeded();
    const compact = await measureHierarchy(page);

    // Recreate the previous sizing to compare actual geometry, not just CSS declarations.
    await page.locator(".dispatch-illustration").evaluate(figure => {
      figure.style.width = "min(100%, 72rem)";
      const ecosystem = figure.querySelector(".workflow-ecosystem");
      ecosystem.style.width = "100%";
      ecosystem.style.zoom = "1";
    });
    const original = await measureHierarchy(page);
    await page.locator(".dispatch-illustration").evaluate(figure => {
      figure.removeAttribute("style");
      figure.querySelector(".workflow-ecosystem").removeAttribute("style");
    });
    const desktop = width > 1152;
    for (const [element, dimension] of [["board", "width"], ["board", "height"], ["card", "width"], ["card", "height"]]) {
      expect(compact[element][dimension] / original[element][dimension]).toBeCloseTo(desktop ? 0.8 : 1, 2);
    }
    expect(compact.heading.width).toBe(original.heading.width);
    expect(compact.heading.height).toBe(original.heading.height);
    expect(compact.padding).toBe(original.padding);
    expect(compact.zoom).toBe(desktop ? 0.8 : 1);
    expect(compact.figure.bottom).toBeCloseTo(compact.board.bottom, 1);
    expect(compact.board.x - compact.section.x).toBeCloseTo(compact.section.right - compact.board.right, 1);
    expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(width);
    if (desktop) {
      expect(compact.board.width).toBeLessThanOrEqual(896);
      expect(compact.board.height).toBeLessThanOrEqual(397);
    }
    if (width === 390 || width === 1440) {
      const path = testInfo.outputPath(`hierarchy-landing-${width}.png`);
      await page.locator(".dispatch-story").screenshot({ path });
      await testInfo.attach("Hierarchy landing section", { path, contentType: "image/png" });
    }
  });
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 1280, height: 480 }]) {
  test(`compact hierarchy fits the slideshow at ${viewport.width}x${viewport.height}`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("./");
    await page.getByRole("button", { name: "Start slideshow", exact: true }).click();
    const dialog = page.locator("#landing-slideshow");
    await expect(dialog).toHaveAttribute("data-slide-index", "0");
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("ArrowRight");
    await expect(dialog).toHaveAttribute("data-slide-index", "2");
    await expect.poll(async () => {
      const { section, figure, board } = await measureHierarchy(page);
      return section.x >= -1 && section.right <= viewport.width + 1
        && section.bottom <= viewport.height + 1 && figure.bottom <= section.bottom
        && Math.abs(figure.bottom - board.bottom) < 1;
    }).toBe(true);
    const layout = await measureHierarchy(page);
    expect(layout.zoom).toBe(0.8);
    expect(layout.board.x - layout.section.x).toBeCloseTo(layout.section.right - layout.board.right, 1);
    const path = testInfo.outputPath(`hierarchy-slideshow-${viewport.width}.png`);
    await page.screenshot({ path });
    await testInfo.attach("Hierarchy slideshow", { path, contentType: "image/png" });
  });
}
