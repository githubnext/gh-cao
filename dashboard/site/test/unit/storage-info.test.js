import { afterEach, describe, expect, it } from 'vitest';
import { renderStorageInfo } from '../../src/components/storage-info.js';

const context = {
  pageId: 'any-page', title: 'Data storage', sourceNames: [], sources: {},
  contextDetails: [], headingTag: /** @type {const} */ ('h2')
};

/** @param {string} name @param {string} content */
function meta(name, content) {
  const element = document.createElement('meta');
  element.name = name;
  element.content = content;
  document.head.append(element);
}

afterEach(() => {
  document.head.querySelectorAll('meta[name^="dashboard-"]').forEach((element) => element.remove());
});

describe('deployment storage information', () => {
  it('identifies the static dashboard database as browser-local IndexedDB, not localStorage', () => {
    const root = renderStorageInfo(context);
    expect(root.getAttribute('aria-label')).toBe('Data storage');
    expect(root.textContent).toContain('Browser-local storage (IndexedDB)');
    expect(root.textContent).toContain('not in localStorage');
  });

  it.each([
    ['postgres', 'Server storage (PostgreSQL)']
  ])('uses declared %s deployment configuration, not supplied activity rows', (backend, label) => {
    meta('dashboard-data-backend', 'server-http');
    meta('dashboard-storage-backend', backend);
    expect(renderStorageInfo(context).textContent).toContain(label);
    expect(renderStorageInfo(context).textContent).toContain('not in this browser');
  });

  it.each([undefined, 'unrecognized'])('does not guess the server engine when configuration is %s', (backend) => {
    meta('dashboard-data-backend', 'server-http');
    if (backend) meta('dashboard-storage-backend', backend);
    expect(renderStorageInfo(context).textContent).toContain('Storage engine unknown');
  });
});
