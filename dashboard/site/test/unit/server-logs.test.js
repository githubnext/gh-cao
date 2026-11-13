// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

const { fetchServerLogs, usesRemoteDataBackend } = vi.hoisted(() => ({
  fetchServerLogs: vi.fn(),
  usesRemoteDataBackend: vi.fn(() => true)
}));
vi.mock('../../src/remote-data-backend.js', () => ({ fetchServerLogs, usesRemoteDataBackend }));
const { usesGitHubAuthentication } = vi.hoisted(() => ({
  usesGitHubAuthentication: vi.fn(() => true)
}));
vi.mock('../../src/auth.js', () => ({ usesGitHubAuthentication }));

import { renderServerLogsSetting, renderServerLogsView } from '../../src/components/server-logs.js';

afterEach(() => {
  document.body.replaceChildren();
  vi.clearAllMocks();
  usesRemoteDataBackend.mockReturnValue(true);
  usesGitHubAuthentication.mockReturnValue(true);
});

describe('administrator server logs', () => {
  it('does not show or request logs on Pages or the local server', () => {
    usesRemoteDataBackend.mockReturnValue(false);
    expect(renderServerLogsSetting(() => {})).toBeNull();
    usesRemoteDataBackend.mockReturnValue(true);
    usesGitHubAuthentication.mockReturnValue(false);
    expect(renderServerLogsSetting(() => {})).toBeNull();
    expect(fetchServerLogs).not.toHaveBeenCalled();
  });

  it('hides the hosted settings entry without admin access', async () => {
    fetchServerLogs.mockRejectedValue(new Error('administrator access is required'));
    const section = renderServerLogsSetting(() => {});
    if (!section) throw new Error('server logs setting did not render');
    document.body.append(section);
    await vi.waitFor(() => expect(fetchServerLogs).toHaveBeenCalledOnce());
    expect(section.hidden).toBe(true);
  });

  it('opens only for authorized viewers and displays escaped full-width preformatted logs', async () => {
    fetchServerLogs.mockResolvedValue({ logs: [{ message: '<script>alert(1)</script>' }], operational: { status: 'ok' } });
    const open = vi.fn();
    const section = renderServerLogsSetting(open);
    if (!section) throw new Error('server logs setting did not render');
    document.body.append(section);
    await vi.waitFor(() => expect(section.hidden).toBe(false));
    expect(section.querySelector('h3')?.textContent).toBe('Logs');
    section.querySelector('button')?.click();
    expect(open).toHaveBeenCalledOnce();

    const back = vi.fn();
    const view = renderServerLogsView(back);
    document.body.append(view);
    await vi.waitFor(() => expect(view.querySelector('pre code')?.textContent).toContain('<script>alert(1)</script>'));
    expect(view.querySelector('script')).toBeNull();
    expect(view.querySelector('pre')?.textContent).toContain('"operational"');
    view.querySelector('button')?.click();
    expect(back).toHaveBeenCalledOnce();
  });

  it('reports failed refreshes and aborts in-flight requests on removal', async () => {
    fetchServerLogs.mockResolvedValueOnce({ logs: [] })
      .mockRejectedValueOnce(new Error('administrator access is required'));
    const view = renderServerLogsView(() => {});
    document.body.append(view);
    await vi.waitFor(() => expect(view.querySelector('pre')).not.toBeNull());
    view.querySelectorAll('button')[1].click();
    await vi.waitFor(() => expect(view.querySelector('[role="alert"]')?.textContent).toContain('administrator access is required'));
    fetchServerLogs.mockImplementationOnce(() => new Promise(() => {}));
    view.querySelectorAll('button')[1].click();
    const signal = fetchServerLogs.mock.lastCall?.[0];
    if (!signal) throw new Error('server logs request was not made');
    view.remove();
    await vi.waitFor(() => expect(signal.aborted).toBe(true));
  });

  it('morphs the log body in place across refreshes', async () => {
    fetchServerLogs.mockResolvedValue({ logs: [] });
    const view = renderServerLogsView(() => {});
    document.body.append(view);
    await vi.waitFor(() => expect(view.querySelector('pre')).not.toBeNull());
    const body = view.querySelector('.memory-file-body');
    view.querySelectorAll('button')[1].click();
    await vi.waitFor(() => expect(fetchServerLogs).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(view.querySelector('pre')).not.toBeNull());
    expect(view.querySelector('.memory-file-body')).toBe(body);
  });
});
