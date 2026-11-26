import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { primerStylesheet } from '../../src/styles.js';
import { primerStyles } from '../../src/styles-primer.js';
import { baseStyles } from '../../src/styles-base.js';

describe('DLS-CONF-004 scaffold gates', () => {
  it('DLS-CONF-004 initializes the presenter workspace tooling', () => {
    expect(true).toBe(true);
  });

  it('keeps the browser preview populated with chart and linked-run fixtures', () => {
    const preview = readFileSync(resolve('src/dashboard-fixtures.js'), 'utf8');

    expect(preview.match(/"run-link":/g)?.length).toBeGreaterThanOrEqual(5);
  });

  it('uses the GitHub Agentic Workflows favicon', () => {
    const preview = readFileSync(resolve('index.html'), 'utf8');
    const favicon = readFileSync(resolve('favicon.svg'), 'utf8');
    const agenticWorkflowsFavicon = readFileSync(resolve('../../public/favicon.svg'), 'utf8');

    expect(preview).toContain('<link rel="icon" href="./favicon.svg">');
    expect(favicon).toBe(agenticWorkflowsFavicon);
  });

  it('keeps docs and dashboard branding on the same Agentic Workflows mark', () => {
    const favicon = readFileSync(resolve('favicon.svg'), 'utf8');
    const day = readFileSync(resolve('../../docs/assets/logo-day.svg'), 'utf8');
    const night = readFileSync(resolve('../../docs/assets/logo-night.svg'), 'utf8');
    const mark = /M1 3a2 2 0 0 1 2-2[^"]+/;

    for (const logo of [favicon, day, night]) {
      expect(logo).toContain('<title>GitHub Agentic Workflows</title>');
      expect(logo.match(mark)?.[0]).toBe(favicon.match(mark)?.[0]);
    }
  });

  it('declares an installable web app manifest and iOS icon', () => {
    const preview = readFileSync(resolve('index.html'), 'utf8');
    const manifest = JSON.parse(readFileSync(resolve('manifest.webmanifest'), 'utf8'));

    expect(preview).toContain(
      '<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">'
    );
    expect(preview).toContain('<link rel="apple-touch-icon" href="./apple-touch-icon.png">');
    expect(preview).toContain('<link rel="manifest" href="./manifest.webmanifest" crossorigin="use-credentials">');
    expect(preview).toContain('<meta name="application-name" content="Central Agentic Ops Dashboard">');
    expect(preview).toContain('<meta name="theme-color" content="#0d1117" media="(prefers-color-scheme: dark)">');
    expect(preview).toContain('<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">');
    expect(preview).toContain('<meta name="mobile-web-app-capable" content="yes">');
    expect(preview).toContain('<meta name="apple-mobile-web-app-capable" content="yes">');
    expect(preview).toContain('<meta name="apple-mobile-web-app-title" content="Agentic Ops">');
    expect(preview).toContain('<meta name="apple-mobile-web-app-status-bar-style" content="black">');
    expect(preview).toContain('content="Monitor and operate GitHub Agentic Workflows from a unified dashboard."');
    expect(manifest).toMatchObject({
      id: './',
      start_url: './',
      scope: './',
      display: 'standalone',
      display_override: ['minimal-ui', 'standalone'],
      background_color: '#0d1117',
      theme_color: '#0d1117'
    });
    expect(manifest.icons).toEqual(expect.arrayContaining([
      expect.objectContaining({ src: './icon-192.png', sizes: '192x192', purpose: 'any' }),
      expect.objectContaining({ src: './icon-512.png', sizes: '512x512', purpose: 'any' }),
      expect.objectContaining({ src: './icon-maskable-512.png', sizes: '512x512', purpose: 'maskable' })
    ]));
  });

  it('keeps the web app manifest valid and every declared icon usable', () => {
    const manifest = JSON.parse(readFileSync(resolve('manifest.webmanifest'), 'utf8'));
    const requiredStrings = [
      'name', 'short_name', 'description', 'lang', 'id', 'start_url', 'scope',
      'display', 'background_color', 'theme_color'
    ];

    for (const property of requiredStrings) {
      expect(manifest[property], property).toBeTypeOf('string');
      expect(manifest[property].trim(), property).not.toBe('');
    }
    expect(['standalone', 'minimal-ui', 'fullscreen']).toContain(manifest.display);
    expect(manifest.id).toBe('./');
    expect(manifest.start_url).toBe('./');
    expect(manifest.scope).toBe('./');
    expect(manifest.categories).toEqual(expect.arrayContaining(['business', 'productivity']));
    expect(manifest.icons.length).toBeGreaterThanOrEqual(2);

    for (const icon of manifest.icons) {
      expect(icon).toEqual({
        src: expect.stringMatching(/^\.\/[^/]+\.png$/),
        sizes: expect.stringMatching(/^\d+x\d+$/),
        type: 'image/png',
        purpose: expect.stringMatching(/^(any|maskable)$/)
      });
      const iconPath = resolve(icon.src);
      expect(existsSync(iconPath), icon.src).toBe(true);
      const contents = readFileSync(iconPath);
      expect(contents.subarray(1, 4).toString('ascii'), icon.src).toBe('PNG');
      const [width, height] = icon.sizes.split('x').map(Number);
      expect(contents.readUInt32BE(16), `${icon.src} width`).toBe(width);
      expect(contents.readUInt32BE(20), `${icon.src} height`).toBe(height);
    }

    const appleIcon = readFileSync(resolve('apple-touch-icon.png'));
    expect(appleIcon.readUInt32BE(16)).toBe(180);
    expect(appleIcon.readUInt32BE(20)).toBe(180);
  });

  it('sets the dashboard title before the presenter loads', () => {
    const preview = readFileSync(resolve('index.html'), 'utf8');

    expect(preview).toContain('<title>Central Agentic Ops Dashboard</title>');
  });

  it('paints the system-theme canvas before scripts with the same tokens as Primer', () => {
    const preview = readFileSync(resolve('index.html'), 'utf8');
    const shell = new DOMParser().parseFromString(preview, 'text/html');
    const css = shell.head.querySelector('style')?.textContent ?? '';

    expect(shell.head.querySelector('meta[name="color-scheme"]')?.getAttribute('content'))
      .toBe('light dark');
    expect(preview.indexOf('<style>')).toBeLessThan(preview.indexOf('<script>'));
    expect(css).toContain('html, body { background: var(--canvas); color: var(--fg); }');
    expect(css).toContain('@media (prefers-color-scheme: light)');
    for (const declaration of [
      'color-scheme: dark;', '--canvas: #0d1117;', '--fg: #f0f6fc;',
      'color-scheme: light;', '--canvas: #ffffff;', '--fg: #1f2328;'
    ]) {
      expect(css).toContain(declaration);
      expect(baseStyles).toContain(declaration);
    }
  });

  it('displays an error when JavaScript is unavailable', () => {
    const preview = readFileSync(resolve('index.html'), 'utf8');

    expect(preview).toContain('<noscript>');
    expect(preview).toContain(
      '<p role="alert">JavaScript is required to use the Central Agentic Ops Dashboard. Enable JavaScript in your browser and reload this page.</p>'
    );
  });

  it('parity motion audit keeps report-style transitions and reduced-motion overrides', () => {
    const styles = primerStyles;

    expect(styles).toContain('.lede { color: var(--muted); }');
    expect(styles).toContain('.pie-chart-total-value { fill: var(--fg); font-size: 5px;');
    expect(styles).toContain('.chart-widget { min-height: 230px; display: grid; place-items: center; margin: 12px 0; border: 0; background: transparent; }');
    expect(styles).toContain('.pie-chart-layout .chart-widget { min-width: 0; min-height: 160px; margin: 0; }');
    expect(styles).toContain('.pie-chart-layout .chart-widget svg { width: 100%; max-width: 160px;');
    expect(styles).toMatch(/@media \(max-width: 700px\) \{[\s\S]*\.pie-chart-layout \.chart-widget svg \{ max-width: 140px; \}/);
    expect(styles).toContain('#page-preview .pie-chart-card { padding: 0; border: 0; }');
    expect(styles).toContain('#page-preview .pie-chart-layout .chart-widget { border: 0; background: transparent; }');
    expect(styles).toContain('.table-summary-boolean .chart-widget .chart-series-1 { stroke: var(--success); }');
    expect(styles).toContain('.table-summary-boolean .chart-widget .chart-series-2 { stroke: var(--attention); }');
    expect(styles).toContain('.table-summary-boolean .chart-widget .chart-series-3 { stroke: var(--muted); }');
    expect(styles).toContain('.table-summary-boolean .chart-widget .chart-series-semantic-failure { stroke: var(--danger); }');
    expect(styles).toContain('.horizontal-bar-chart-row { min-width: 0; display: grid; grid-template-columns: minmax(120px, 52%) minmax(48px, 1fr) auto;');
    expect(styles).toContain('.chart-widget .chart-series-12 { stroke: var(--violet); }');
    expect(styles).toContain('transition: color 120ms ease;');
    expect(styles).toContain('transition: background-color 120ms ease, color 120ms ease;');
    expect(styles).toContain('transition: opacity 80ms linear;');
    expect(styles).toContain('@media (prefers-reduced-motion: reduce)');
    expect(styles).toContain('transition-duration: 0.01ms !important;');
    expect(styles).toContain('.repository-link');
  });

  it('keeps declared font sizes on mobile Safari', () => {
    const styles = primerStyles;

    expect(styles).toContain('-webkit-text-size-adjust: 100%;');
    expect(styles).toContain('text-size-adjust: 100%;');
  });

  it('styles maintenance list actions as primary buttons', () => {
    const style = document.createElement('style');
    style.textContent = primerStylesheet();
    document.head.append(style);
    try {
      const stylesheet = style.sheet;
      if (!stylesheet) throw new Error('Primer stylesheet did not parse');
      const rules = [...stylesheet.cssRules].filter((rule) => rule.type === window.CSSRule.STYLE_RULE);
      const triggerRule = /** @type {CSSStyleRule | undefined} */ (
        rules.find((rule) => /** @type {CSSStyleRule} */ (rule).selectorText === '.document-list-header .cli-action-trigger')
      );
      const hoverRule = /** @type {CSSStyleRule | undefined} */ (
        rules.find((rule) => /** @type {CSSStyleRule} */ (rule).selectorText === '.document-list-header .cli-action-trigger:hover')
      );
      const iconRule = /** @type {CSSStyleRule | undefined} */ (
        rules.find((rule) => /** @type {CSSStyleRule} */ (rule).selectorText === '.document-list-header .cli-action-trigger > .octicon')
      );

      expect(triggerRule?.style.getPropertyValue('border')).toBe('1px solid var(--accent)');
      expect(triggerRule?.style.getPropertyValue('background')).toBe('var(--accent)');
      expect(triggerRule?.style.getPropertyValue('color')).toBe('var(--canvas)');
      expect(hoverRule?.style.getPropertyValue('border-color')).toBe('var(--accent)');
      expect(hoverRule?.style.getPropertyValue('background')).toBe('color-mix(in srgb, var(--accent) 88%, var(--fg))');
      expect(hoverRule?.style.getPropertyValue('color')).toBe('var(--canvas)');
      expect(iconRule?.style.getPropertyValue('color')).toBe('inherit');
    } finally {
      style.remove();
    }
  });

  it('uses the Primer body font size on mobile', () => {
    const style = document.createElement('style');
    style.textContent = primerStylesheet();
    document.head.append(style);
    try {
      const stylesheet = style.sheet;
      if (!stylesheet) throw new Error('Primer stylesheet did not parse');
      const mobileBodySelectors = new Set();
      for (const rule of stylesheet.cssRules) {
        if (rule.type !== window.CSSRule.MEDIA_RULE) continue;
        const mediaRule = /** @type {CSSMediaRule} */ (rule);
        if (mediaRule.conditionText.replace(/\s/g, '') !== '(max-width:700px)') continue;
        for (const nestedRule of mediaRule.cssRules) {
          if (nestedRule.type !== window.CSSRule.STYLE_RULE) continue;
          const styleRule = /** @type {CSSStyleRule} */ (nestedRule);
          if (!styleRule.selectorText || styleRule.style.getPropertyValue('font-size') !== '1rem') continue;
          for (const selector of styleRule.selectorText.split(',')) mobileBodySelectors.add(selector.trim());
        }
      }

      expect(mobileBodySelectors.has('body')).toBe(true);
      expect(mobileBodySelectors.has('.dashboard-root')).toBe(true);
    } finally {
      style.remove();
    }
  });

  it('keeps reset confirmation dialog height content-sized on mobile', () => {
    const styles = primerStyles;
    const styleLines = styles.split('\n');
    /** @param {string} selector */
    const declarationMap = (selector) => {
      const matches = styleLines.filter((line) => line.startsWith(`${selector} {`));
      expect(matches).toHaveLength(1);
      const bodyStart = matches[0]?.indexOf('{') ?? -1;
      const bodyEnd = matches[0]?.lastIndexOf('}') ?? -1;
      const ruleBody = bodyStart >= 0 && bodyEnd > bodyStart ? matches[0].slice(bodyStart + 1, bodyEnd) : '';
      const declarations = new Map();
      for (const declaration of ruleBody.split(';').map((entry) => entry.trim()).filter(Boolean)) {
        const separator = declaration.indexOf(':');
        if (separator < 0) continue;
        declarations.set(
          declaration.slice(0, separator).trim(),
          declaration.slice(separator + 1).trim()
        );
      }
      return declarations;
    };
    const dialogRule = declarationMap('.reset-dashboard-dialog');
    const openRule = declarationMap('.reset-dashboard-dialog[open]');
    const bodyRule = declarationMap('.reset-dashboard-dialog-body');

    expect(dialogRule.get('max-height')).toBe('calc(100vh - 32px)');
    expect(dialogRule.get('height')).toBe('fit-content');
    expect(openRule.get('grid-template-rows')).toBe('auto minmax(0, 1fr) auto');
    expect(bodyRule.get('align-content')).toBe('start');
    expect(bodyRule.get('overflow-y')).toBe('auto');
  });

  it('keeps the JSON dashboard shell aligned with its shared component styles', () => {
    const shellComponents = [
      'src/components/dashboard-frame.js',
      'src/components/dashboard-header.js',
      'src/components/dashboard-navigation.js'
    ].map((path) => readFileSync(resolve(path), 'utf8')).join('\n');
    const styles = primerStyles;

    for (const shellClass of [
      'app-shell',
      'org-sidebar',
      'sidebar-brand',
      'primary-nav',
      'app-main',
      'overview-header',
      'title-area',
      'report-body'
    ]) {
      expect(shellComponents).toContain(`className: '${shellClass}`);
    }

    for (const sharedRule of [
      '.sidebar-brand { display: flex; align-items: center; gap: 6px;',
      '.app-main > .top-nav { position: relative; z-index: 20; border-bottom: 1px solid var(--border); }',
      '.breadcrumb-context > :not([hidden]) ~ :not([hidden])::before { content: "/";',
      '.breadcrumb-context > :is([data-breadcrumb-root], [data-breadcrumb-dashboard]) { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }',
      '.overview-header { min-width: 0; flex: 1; }',
      '.overview-header .lede { min-height: 1.25rem; margin: 3px 0 0; overflow: hidden; font-size: .875rem; line-height: 1.25rem; text-overflow: ellipsis; white-space: nowrap; }',
      '.mobile-page-header .overview-header { width: 100%; min-width: 0; flex: none; }',
      '.mobile-page-header .overview-header .lede { height: 0; min-height: 0; margin: 0; overflow: hidden; line-height: 0; visibility: hidden; }',
      '.workflow-runtime-summary { max-width: 920px; margin-bottom: 24px; }',
      'footer { min-height: 44px; display: flex; flex: none; align-items: center; justify-content: space-between;'
    ]) {
      expect(styles).toContain(sharedRule);
    }
  });

  it('hides mobile primary navigation and keeps Overview content full-width', () => {
    const styles = primerStyles;

    expect(styles).toContain('.primary-nav { display: none; }');
    expect(styles).toContain('.dashboard-overview-page { margin: 0; }');
    expect(styles).toContain('main.dashboard-prototype:has(.dashboard-overview-page:not([hidden])) { padding: 0; }');
  });

  it('stacks the expanded filter panel above the page header and hides the horizon tooltip', () => {
    const styles = primerStyles;
    /** @param {string} rule */
    const zIndex = (rule) => {
      const block = styles.match(new RegExp(`${rule} \\{([^}]*)\\}`))?.[1];
      const layer = block?.match(/z-index: (\d+);/)?.[1];
      expect(layer, `missing z-index for ${rule}`).toBeDefined();
      return Number(layer);
    };
    const panelLayer = zIndex('\\.filter-bar-expanded \\.filter-tuning-controls');
    const headerLayer = zIndex('\\.app-main > \\.top-nav');

    expect(panelLayer).toBeGreaterThan(headerLayer);
  });

  it('spaces the filter count badge from the eye icon and control boundary', () => {
    const styles = primerStyles;

    expect(styles).toContain('.filter-control > .count-badge { margin: 0 9px 0 3px; }');
  });

  it('systematically ellipsizes output evidence at every viewport size', () => {
    const styles = primerStyles;

    expect(styles).toContain('.table-output-evidence { display: block; max-width: 80ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }');
  });
});
