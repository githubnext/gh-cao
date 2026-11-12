// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  const module = await import('../../src/components/storage-info.js');
  return { ...module, output };
}

/** @param {string | null} content */
function declare(content) {
  document.head.innerHTML = content === null ? '' : `<meta name="dashboard-storage-backend" content="${content}">`;
}

const context = /** @type {any} */ ({ title: 'Storage' });

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
  document.head.innerHTML = '';
});

describe('storage-info debug logging', () => {
  it('is silent by default', async () => {
    declare('postgres');
    const { renderStorageInfo, output } = await load('');
    renderStorageInfo(context);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs the resolved backend under the storage-info category', async () => {
    declare('postgres');
    const { renderStorageInfo, output } = await load('?debug=storage-info');
    renderStorageInfo(context);
    expect(output.debug).toHaveBeenCalledWith('[cao:storage-info]', { event: 'backend-resolved', declared: true, backend: 'postgres' });
  });

  it('never logs an arbitrary declared value', async () => {
    declare('secret-token-value');
    const { renderStorageInfo, output } = await load('?debug=storage-info');
    renderStorageInfo(context);
    expect(output.debug).toHaveBeenCalledWith('[cao:storage-info]', { event: 'backend-resolved', declared: true, backend: 'unrecognized' });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret');
  });
});
