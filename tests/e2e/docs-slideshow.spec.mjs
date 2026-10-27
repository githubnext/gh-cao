import { expect, test } from "@playwright/test";

const presentation = page => page.locator('dialog#landing-slideshow[aria-label="Central Agentic Ops presentation"]');
const runtimeAsset = /\/slideshow\.[^/]+\.js(?:\?|$)/;
const slideTitles = [
  "Central Agentic Ops",
  "Monitor and govern GitHub Agentic Workflows at scale.",
  "Shared workflows. Clear ownership.",
  "Easy agentic workflow upkeep.",
  "Roll out with confidence.",
  "Use evidence to decide what scales.",
  "Webhook Gateway",
  "Built on Agentic Workflows",
];

async function openSlideshow(page) {
  await page.getByRole("button", { name: "Start slideshow", exact: true }).click();
  await expect(presentation(page)).toBeVisible();
}

async function expectSlide(page, index) {
  await expect(presentation(page)).toHaveAttribute("data-slide-index", String(index));
  await expect(presentation(page).locator("[data-slideshow-status]")).toHaveText(`${index + 1} / ${slideTitles.length} — ${slideTitles[index]}`);
  await expect(presentation(page).locator("[data-slideshow-slide]:visible")).toHaveCount(1);
}

async function draw(page) {
  const bounds = await presentation(page).locator("[data-slideshow-ink]").boundingBox();
  await page.mouse.move(bounds.x + bounds.width * 0.3, bounds.y + bounds.height * 0.3);
  await page.mouse.down();
  await page.mouse.move(bounds.x + bounds.width * 0.6, bounds.y + bounds.height * 0.55, { steps: 5 });
  await page.mouse.up();
}

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ reducedMotion: "reduce" });
});

test("presents existing landing content with bounded keyboard and button navigation", async ({ page }) => {
  await page.goto("./");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(slideTitles[1]);
  const originalDeck = await page.locator("[data-slideshow-slides]").elementHandle();
  await openSlideshow(page);
  await expectSlide(page, 0);
  expect(await presentation(page).locator(".slideshow-controls button svg").evaluateAll(icons => icons.every(icon => icon.childElementCount > 0))).toBe(true);
  expect(await presentation(page).locator("[data-slideshow-slides]").evaluate((deck, original) => deck === original, originalDeck)).toBe(true);
  await expect(page.getByRole("button", { name: "Previous slide", exact: true })).toBeDisabled();
  for (let index = 1; index < slideTitles.length; index++) {
    await page.getByRole("button", { name: "Next slide", exact: true }).click();
    await expectSlide(page, index);
    if (index === 1) {
      await expect(presentation(page).locator(".hero-action:visible")).toHaveCount(0);
    }
  }
  await expect(page.getByRole("button", { name: "Next slide", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Exit slideshow", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expectSlide(page, slideTitles.length - 1);
  for (const [key, index] of [["Home", 0], ["PageDown", 1], ["ArrowDown", 2], ["PageUp", 1], ["ArrowLeft", 0], ["End", slideTitles.length - 1]]) {
    await page.keyboard.press(key);
    await expectSlide(page, index);
  }
  await page.keyboard.press("Escape");
  await expect(presentation(page)).not.toBeVisible();
  await expect(page.getByRole("button", { name: "Start slideshow", exact: true })).toBeFocused();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(slideTitles[1]);
  await expect(page.locator(".hero-action:visible")).toHaveCount(3);
  await expect(page.locator(".product-story:visible")).toHaveCount(slideTitles.length - 2);
  await expect(page.locator("[data-snippet-trigger]")).toHaveCount(0);
  await expect(page.locator("[data-slideshow-slides]")).not.toHaveAttribute("style", /transform|slide-width|slide-height/);
});

test("loads the runtime once on demand and reports import failures without trapping the launcher", async ({ page }) => {
  const requests = [];
  page.on("request", request => { if (runtimeAsset.test(request.url())) requests.push(request.url()); });
  await page.goto("./");
  await expect(page.getByRole("button", { name: "Start slideshow", exact: true })).toBeVisible();
  expect(requests).toHaveLength(0);
  await openSlideshow(page);
  expect(requests).toHaveLength(1);
  await page.keyboard.press("Escape");
  await openSlideshow(page);
  expect(requests).toHaveLength(1);
  await page.keyboard.press("Escape");

  await page.route(runtimeAsset, route => route.abort());
  await page.reload();
  await page.getByRole("button", { name: "Start slideshow", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveText("Unable to load the slideshow. Reload the page and try again.");
  await expect(page.getByRole("button", { name: "Start slideshow", exact: true })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Start slideshow", exact: true })).not.toHaveAttribute("aria-busy");
  await expect(presentation(page)).not.toBeVisible();
});

test("keeps drawings per slide, supports shapes and undo, and resets them on exit", async ({ page }) => {
  await page.goto("./");
  await openSlideshow(page);
  await page.getByRole("button", { name: "Drawing tools", exact: true }).click();
  await page.getByRole("button", { name: "Cyan ink", exact: true }).click();
  await page.getByRole("button", { name: "Draw rectangle (Shift for square)", exact: true }).click();
  await page.keyboard.down("Shift");
  await draw(page);
  await page.keyboard.up("Shift");
  const ink = presentation(page).locator("[data-slideshow-ink]");
  await expect(ink.locator('[data-drawing-kind="rect"]')).toHaveCount(1);
  const rect = ink.locator('rect[stroke="#00d9ff"]');
  expect(Number(await rect.getAttribute("width"))).toBeCloseTo(Number(await rect.getAttribute("height")), 1);
  await page.getByRole("button", { name: "Next slide", exact: true }).click();
  await expect(ink.locator("g")).toHaveCount(0);
  await page.getByRole("button", { name: "Draw arrow", exact: true }).click();
  await draw(page);
  await expect(ink.locator('[data-drawing-kind="arrow"]')).toHaveCount(1);
  await page.keyboard.press("ControlOrMeta+z");
  await expect(ink.locator("g")).toHaveCount(0);
  await page.getByRole("button", { name: "Eyes emoji", exact: true }).click();
  await draw(page);
  await expect(ink.locator("text")).toHaveText("👀");
  await page.getByRole("button", { name: "Clear slide drawings", exact: true }).click();
  await expect(ink.locator("g")).toHaveCount(0);
  await page.getByRole("button", { name: "Previous slide", exact: true }).click();
  await expect(ink.locator('[data-drawing-kind="rect"]')).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(presentation(page)).toBeVisible();
  await expect(ink).toHaveAttribute("data-tool", "pointer");
  await expect(ink.locator("g")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(presentation(page)).not.toBeVisible();
  await openSlideshow(page);
  await expect(ink.locator("g")).toHaveCount(0);
});

test("expands and edits code locally without changing the original commands or navigating slides", async ({ page }) => {
  await page.goto("./");
  const original = await page.locator(".terminal code").allTextContents();
  await openSlideshow(page);
  const setupSlide = slideTitles.indexOf("Roll out with confidence.");
  for (let index = 0; index < setupSlide; index++) await page.keyboard.press("ArrowRight");
  await expectSlide(page, setupSlide);
  const snippet = presentation(page).getByRole("button", { name: "Expand gh cao setup", exact: true });
  await snippet.focus();
  await page.keyboard.press("Enter");
  const expanded = page.getByRole("dialog", { name: "Expanded snippet", exact: true });
  await expect(expanded).toBeVisible();
  const editor = expanded.getByRole("textbox");
  await editor.fill("gh cao doctor");
  await page.keyboard.press("Home");
  await expectSlide(page, setupSlide);
  await page.keyboard.press("Escape");
  await expect(expanded).not.toBeVisible();
  await expect(snippet).toBeFocused();
  expect(await page.locator(".terminal code").allTextContents()).toEqual(original);
  await snippet.click();
  await expect(editor).toHaveText("gh cao setup");
  await page.getByRole("button", { name: "Close expanded snippet", exact: true }).click();
  await page.keyboard.press("ArrowLeft");
  await expectSlide(page, slideTitles.indexOf("Easy agentic workflow upkeep."));
  await page.getByRole("button", { name: "Expand Dashboard maintenance", exact: true }).click();
  const maintenance = page.getByRole("dialog", { name: "Dashboard maintenance", exact: true });
  await expect(maintenance).toBeVisible();
  await expect(maintenance).toContainText("Upgrade one repository or Upgrade all");
  await expect.poll(() => maintenance.locator("img").evaluate(image => image.complete && image.naturalWidth > 0)).toBe(true);
  await expect(page.locator("[data-snippet-editor]")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await expect(page.locator("[data-snippet-trigger]")).toHaveCount(0);
  expect(await page.locator(".terminal code").allTextContents()).toEqual(original);
});

test("expands the campaign snippet without changing its manifest", async ({ page }) => {
  await page.goto("./");
  await openSlideshow(page);
  await page.keyboard.press("End");
  await expectSlide(page, slideTitles.indexOf("Built on Agentic Workflows"));
  const code = presentation(page).locator("[data-slideshow-slide]:visible pre code");
  const original = await code.textContent();
  const trigger = presentation(page).getByRole("button", { name: "Expand dependabot/aw.yml", exact: true });
  await trigger.click();
  const expanded = page.getByRole("dialog", { name: "dependabot/aw.yml", exact: true });
  await expect(expanded.getByRole("textbox")).toContainText("name: Dependabot");
  await page.keyboard.press("Escape");
  await expect(trigger).toBeFocused();
  await expect(code).toHaveText(original);
});

for (const theme of ["light", "dark"]) {
  test(`reveals the floating toolbar only during interaction in ${theme} mode`, async ({ page }) => {
    await page.addInitScript(value => localStorage.setItem("starlight-theme", value), theme);
    await page.goto("./");
    await openSlideshow(page);
    const controls = presentation(page).getByRole("group", { name: "Slide navigation", exact: true });
    await expect(controls).toHaveCSS("opacity", "0.12");
    await expect(controls).toHaveCSS("transition-duration", "0s");

    await page.emulateMedia({ reducedMotion: "no-preference" });
    await controls.hover();
    await expect(controls).toHaveCSS("opacity", "1");
    await page.mouse.move(0, 0);
    await expect(controls).toHaveCSS("opacity", "0.12");

    await page.keyboard.press("Shift+Tab");
    await expect(page.getByRole("button", { name: "Next slide", exact: true })).toBeFocused();
    await expect(controls).toHaveCSS("opacity", "1");
    await presentation(page).locator("[data-slideshow-viewport]").click({ position: { x: 1, y: 1 } });
    await expect(controls).toHaveCSS("opacity", "0.12");

    await page.getByRole("button", { name: "Next slide", exact: true }).click();
    await page.mouse.move(0, 0);
    await expectSlide(page, 1);
    await expect(controls).toHaveCSS("opacity", "0.12");
    await page.getByRole("button", { name: "Drawing tools", exact: true }).click();
    await page.mouse.move(0, 0);
    await expect(page.getByRole("group", { name: "Draw on slides", exact: true })).toBeVisible();
    await expect(controls).toHaveCSS("opacity", "1");
    await page.getByRole("button", { name: "Drawing tools", exact: true }).click();
    await page.mouse.move(0, 0);
    await expect(controls).toHaveCSS("opacity", "0.12");
  });

  for (const viewport of [{ width: 1440, height: 900 }, { width: 800, height: 600 }, { width: 1280, height: 480 }]) {
    test(`fits all slides to ${viewport.width}x${viewport.height} in ${theme} mode`, async ({ page }, testInfo) => {
      await page.setViewportSize(viewport);
      await page.addInitScript(value => localStorage.setItem("starlight-theme", value), theme);
      await page.goto("./");
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      await openSlideshow(page);
      const frame = await presentation(page).locator("[data-slideshow-viewport]").boundingBox();
      expect(frame).toEqual({ x: 0, y: 0, ...viewport });
      const toolbar = await presentation(page).locator(".slideshow-controls").boundingBox();
      expect(toolbar.x).toBeGreaterThan(0);
      expect(toolbar.x + toolbar.width).toBeLessThan(viewport.width);
      expect(toolbar.width).toBeLessThan(viewport.width - 32);
      expect(toolbar.y).toBeGreaterThan(0);
      expect(toolbar.y + toolbar.height).toBeLessThan(viewport.height);
      for (let index = 0; index < slideTitles.length; index++) {
        await expectSlide(page, index);
        const slide = presentation(page).locator("[data-slideshow-slide]:visible");
        const images = slide.locator('img:not([loading="lazy"])');
        for (const image of await images.all()) {
          await expect.poll(() => image.evaluate(element => element.complete && element.naturalWidth > 0)).toBe(true);
        }
        await expect.poll(async () => {
          const frame = await presentation(page).locator("[data-slideshow-viewport]").boundingBox();
          const bounds = await slide.boundingBox();
          return bounds.x >= frame.x - 1 && bounds.y >= frame.y - 1
            && bounds.x + bounds.width <= frame.x + frame.width + 1
            && bounds.y + bounds.height <= frame.y + frame.height + 1;
        }).toBe(true);
        expect(await slide.evaluate(element => {
          const bounds = element.getBoundingClientRect();
          return [...element.querySelectorAll("h1, h2, .docs-hero-intro, .hero-visual, .phone-preview, .product-story-copy, .terminal, .dispatch-illustration, .product-shot, .workflow-example, .control-diagram, .decision-loop")]
            .filter(child => child.checkVisibility())
            .every(child => {
              const content = child.getBoundingClientRect();
              return content.x >= bounds.x - 1 && content.y >= bounds.y - 1
                && content.right <= bounds.right + 1 && content.bottom <= bounds.bottom + 1;
            });
        })).toBe(true);
        expect(await slide.evaluate(element => getComputedStyle(element).opacity)).toBe("1");
        if (viewport.width === 1440 && index === 1) {
          const path = testInfo.outputPath(`slideshow-desktop-${theme}.png`);
          await page.screenshot({ path });
          await testInfo.attach(`CAO slideshow (${theme})`, { path, contentType: "image/png" });
        }
        if (index < slideTitles.length - 1) await page.keyboard.press("ArrowRight");
      }
    });
  }
}

test("restores scroll and the mobile layout when the viewport becomes narrow", async ({ page }, testInfo) => {
  await page.goto("./");
  const deck = page.locator("[data-slideshow-slides]");
  const parent = await deck.evaluate(element => element.parentElement.className);
  await page.evaluate(() => window.scrollTo(0, 400));
  const scrollY = await page.evaluate(() => window.scrollY);
  await openSlideshow(page);
  await page.keyboard.press("End");
  await page.getByRole("button", { name: "Exit slideshow", exact: true }).click();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(scrollY);
  expect(await deck.evaluate(element => element.parentElement.className)).toBe(parent);
  await openSlideshow(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(presentation(page)).not.toBeVisible();
  await expect(page.getByRole("button", { name: "Start slideshow", exact: true })).toBeHidden();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(slideTitles[1]);
  await page.evaluate(() => window.scrollTo(0, 0));
  expect(await page.locator("html").evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(390);
  const path = testInfo.outputPath("slideshow-mobile-landing.png");
  await page.screenshot({ path, fullPage: true });
  await testInfo.attach("CAO mobile scrolling layout", { path, contentType: "image/png" });
  await page.setViewportSize({ width: 1440, height: 900 });
  await openSlideshow(page);
  await expectSlide(page, 0);
});

test("supports animated rapid navigation, reduced motion changes, and browsers without View Transitions", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await page.goto("./");
  await openSlideshow(page);
  expect(await page.evaluate(() => document.getAnimations().filter(animation => animation.id === "cao-slide-entrance").length)).toBeGreaterThan(0);
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowLeft");
  await expectSlide(page, 1);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect.poll(() => page.evaluate(() => document.getAnimations().filter(animation => animation.id === "cao-slide-entrance").length)).toBe(0);
  await expect(page.locator("html")).not.toHaveAttribute("data-slideshow-direction");
  await page.keyboard.press("End");
  await expectSlide(page, slideTitles.length - 1);
  await page.keyboard.press("Escape");
  await page.evaluate(() => { document.startViewTransition = undefined; });
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await openSlideshow(page);
  await page.keyboard.press("End");
  await expectSlide(page, slideTitles.length - 1);
});

for (const viewTransitions of [true, false]) {
  test(`keeps hero typography stable during entrances ${viewTransitions ? "with" : "without"} View Transitions`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "no-preference" });
    if (!viewTransitions) {
      await page.addInitScript(() => { document.startViewTransition = undefined; });
    }
    await page.goto("./");
    await page.evaluate(() => document.fonts.ready);
    await openSlideshow(page);
    for (const direction of ["next", "previous"]) {
      const frames = await page.evaluate(async direction => {
        const dialog = document.querySelector("#landing-slideshow");
        const entered = new Promise(resolve => dialog.addEventListener("slideshow:slide-change", resolve, { once: true }));
        dialog.querySelector(`[data-slideshow-${direction}]`).click();
        await entered;
        const elements = [...dialog.querySelectorAll(".docs-hero-intro h1, .docs-hero-intro .tagline")];
        const frames = [];
        const start = performance.now();
        do {
          await new Promise(requestAnimationFrame);
          frames.push(elements.map(element => {
            const style = getComputedStyle(element);
            const { x, y, width, height } = element.getBoundingClientRect();
            return {
              x, y, width, height,
              font: style.font,
              transform: style.transform,
              animationName: style.animationName,
              opacity: Number(style.opacity),
              entrances: element.getAnimations().filter(animation => animation.id === "cao-slide-entrance").length,
            };
          }));
        } while (performance.now() - start < 1100);
        return frames;
      }, direction);
      await expectSlide(page, 1);
      expect(frames.some(frame => frame.some(element => element.entrances > 0))).toBe(true);
      expect(frames.some(frame => frame.some(element => element.opacity < 1))).toBe(true);
      for (const frame of frames) {
        frame.forEach((element, index) => {
          expect(element.animationName).toBe("none");
          expect(element.transform).toBe("none");
          expect(element.font).toBe(frames[0][index].font);
          for (const dimension of ["x", "y", "width", "height"]) {
            expect(element[dimension]).toBeCloseTo(frames[0][index][dimension], 1);
          }
        });
      }
      expect(frames.at(-1).every(element => element.opacity === 1 && element.entrances === 0)).toBe(true);
      if (direction === "next") {
        await page.getByRole("button", { name: "Next slide", exact: true }).click();
        await expectSlide(page, 2);
      }
    }
    await page.keyboard.press("Escape");
    await expect(page.locator(".docs-hero-intro h1")).toHaveCSS("animation-name", "hero-item-enter");
  });
}

test("cleans up on page swaps and does not open a disposed lazy launcher", async ({ page }) => {
  await page.goto("./");
  await openSlideshow(page);
  await page.evaluate(() => document.dispatchEvent(new Event("astro:before-swap")));
  await expect(presentation(page)).not.toBeVisible();
  await expect(page.locator(".product-story:visible")).toHaveCount(slideTitles.length - 2);
  await page.evaluate(() => document.dispatchEvent(new Event("astro:page-load")));
  await openSlideshow(page);
  await expectSlide(page, 0);
  await page.keyboard.press("Escape");
  await page.goto("architecture-at-a-glance/");
  await expect(page.locator("[data-slideshow-trigger]")).toHaveCount(0);

  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route(runtimeAsset, async route => { await gate; await route.continue(); });
  await page.goto("./");
  const request = page.waitForRequest(runtimeAsset);
  await page.getByRole("button", { name: "Start slideshow", exact: true }).click();
  await request;
  await page.evaluate(() => document.dispatchEvent(new Event("astro:before-swap")));
  const response = page.waitForResponse(runtimeAsset);
  release();
  await response;
  await expect(page.locator("[data-slideshow-trigger]")).toBeHidden();
  await expect(presentation(page)).not.toBeVisible();
});

test("keeps the scrolling landing page usable without JavaScript", async ({ browser, baseURL }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, baseURL });
  const page = await context.newPage();
  await page.goto("./");
  await expect(page.getByRole("button", { name: "Start slideshow", exact: true })).toBeHidden();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(slideTitles[1]);
  await expect(page.locator(".product-story:visible")).toHaveCount(slideTitles.length - 2);
  await context.close();
});
