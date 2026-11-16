import { createDebug } from "./debug.js";
import { DEFERRED_STYLE_CLASS_SETS } from "./deferred-style-classes.js";

const debugStyles = createDebug("deferred-styles");

/** @type {WeakMap<Document, MutationObserver>} */
const observers = new WeakMap();
/** @type {WeakSet<Document>} */
const scheduled = new WeakSet();

/** @param {Document} document */
function deferredStylesSource(document) {
  const startupStyles = document.querySelector("style[data-dashboard-styles][data-deferred-href]");
  return startupStyles instanceof HTMLStyleElement ? startupStyles : null;
}

/**
 * Applies the built chart and feature stylesheet. Script-inserted stylesheets
 * do not block rendering, so content that needs the sheet keeps pages hidden
 * until it loads instead of painting one unstyled, shifting frame. Unbuilt
 * pages inline every style and declare no deferred sheet, so this is a no-op
 * there.
 *
 * @param {Document} document
 * @param {"rendered" | "idle" | "unclassified" | "requested"} reason
 */
export function requestDeferredStyles(document, reason = "requested") {
  const startupStyles = deferredStylesSource(document);
  if (!startupStyles) return;
  let deferredStyles = document.querySelector("link[data-dashboard-deferred-styles]");
  if (!(deferredStyles instanceof HTMLLinkElement)) {
    const link = document.createElement("link");
    /** @param {"loaded" | "error"} status */
    const settle = (status) => {
      debugStyles({ event: "deferred-styles-settled", status });
      link.dataset.dashboardDeferredStyles = "loaded";
      observers.get(document)?.disconnect();
      observers.delete(document);
      delete document.documentElement.dataset.deferredStyles;
    };
    link.addEventListener("load", () => settle("loaded"), { once: true });
    link.addEventListener("error", () => settle("error"), { once: true });
    link.rel = "stylesheet";
    link.href = startupStyles.dataset.deferredHref ?? "";
    link.dataset.dashboardDeferredStyles = "";
    startupStyles.after(link);
    deferredStyles = link;
    debugStyles({ event: "deferred-styles-requested", reason });
  }
  if (reason === "rendered" && deferredStyles.getAttribute("data-dashboard-deferred-styles") !== "loaded") {
    document.documentElement.dataset.deferredStyles = "pending";
  }
}

/**
 * Keeps chart and feature styles off the startup path: the deferred sheet is
 * requested once the page renders every class that some deferred selector
 * requires. Mutation callbacks run before the next paint, so pages are hidden
 * before that content is first drawn without the sheet. Observation continues
 * until the sheet loads so content rendered during an idle prefetch also waits.
 *
 * @param {Document} document
 * @param {readonly string[]} [classSets]
 */
export function observeDeferredStyles(document, classSets = DEFERRED_STYLE_CLASS_SETS) {
  if (!deferredStylesSource(document) || observers.has(document) || !document.body) return;
  if (!classSets.length) {
    requestDeferredStyles(document, "unclassified");
    return;
  }
  /** @type {Map<string, string[][]>} */
  const setsByClass = new Map();
  for (const classSet of classSets) {
    const classes = classSet.split(" ");
    for (const name of classes) setsByClass.set(name, [...(setsByClass.get(name) ?? []), classes]);
  }
  /** @type {Set<string>} */
  const rendered = new Set();
  /** @param {Element} element */
  const completesSet = (element) => {
    let complete = false;
    for (const name of element.classList) {
      if (rendered.has(name) || !setsByClass.has(name)) continue;
      rendered.add(name);
      complete ||= setsByClass.get(name)?.some((classes) => classes.every((required) => rendered.has(required))) ?? false;
    }
    return complete;
  };
  /** @param {Node} node */
  const needsStyles = (node) => {
    if (!(node instanceof Element)) return false;
    let complete = completesSet(node);
    for (const element of node.getElementsByTagName("*")) complete = completesSet(element) || complete;
    return complete;
  };
  if (needsStyles(document.body)) {
    requestDeferredStyles(document, "rendered");
    if (document.querySelector('link[data-dashboard-deferred-styles="loaded"]')) return;
  }
  const observer = new MutationObserver((records) => {
    let complete = false;
    for (const record of records) {
      if (record.type === "attributes") complete = (record.target instanceof Element && completesSet(record.target)) || complete;
      else for (const node of record.addedNodes) complete = needsStyles(node) || complete;
    }
    if (complete) requestDeferredStyles(document, "rendered");
  });
  observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  observers.set(document, observer);
}

/** Delay after the first data-backed render before prefetching deferred styles. */
export const DEFERRED_STYLES_PREFETCH_DELAY_MS = 3_000;

/**
 * Prefetches the deferred sheet well after startup so later navigation finds
 * it ready without competing with the first meaningful paint.
 *
 * @param {Document} document
 */
export function prefetchDeferredStylesWhenIdle(document) {
  const view = document.defaultView;
  if (!view || !observers.has(document) || scheduled.has(document)) return;
  scheduled.add(document);
  view.setTimeout(() => {
    const request = () => requestDeferredStyles(document, "idle");
    if (typeof view.requestIdleCallback === "function") view.requestIdleCallback(request, { timeout: 2_000 });
    else request();
  }, DEFERRED_STYLES_PREFETCH_DELAY_MS);
}
