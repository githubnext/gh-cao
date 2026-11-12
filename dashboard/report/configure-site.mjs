#!/usr/bin/env node

import { readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { actionsLog as log } from "../../.github/workflows/shared/actions-log.mjs";

const DEFAULT_FAVICON_LINK = '<link rel="icon" href="./favicon.svg">';

function escapeAttribute(value) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

export function configureSite(html, settings) {
  const favicon = settings?.web?.favicon ?? "./favicon.svg";
  if (typeof favicon !== "string") throw new Error("web.favicon must be a string");
  if (!html.includes(DEFAULT_FAVICON_LINK)) throw new Error("dashboard favicon declaration is missing");
  return html.replace(DEFAULT_FAVICON_LINK, `<link rel="icon" href="${escapeAttribute(favicon)}">`);
}

async function main([htmlPath, settingsPath]) {
  if (!htmlPath || !settingsPath) {
    throw new Error("usage: configure-site.mjs <index.html> <control-settings.json>");
  }
  log.group`Configure dashboard site`;
  try {
    const [html, settingsSource] = await Promise.all([
      readFile(htmlPath, "utf8"),
      readFile(settingsPath, "utf8"),
    ]);
    await writeFile(htmlPath, configureSite(html, JSON.parse(settingsSource)));
    log.info`Configured ${htmlPath}`;
  } finally {
    log.endGroup();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main(process.argv.slice(2)).catch((error) => {
    log.error`${error.stack || error.message || error}`;
    process.exitCode = 1;
  });
}
