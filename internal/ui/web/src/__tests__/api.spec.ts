import { describe, it, expect, beforeEach, vi } from 'vitest';
import * as api from '../api';

describe('api', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('getState returns parsed ServerState on 200', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ schema_version: 1, install_id: 'x', onboarding_status: 'pending', completed_steps: ['modelserver_login'] }),
    } as Response);
    const s = await api.getState();
    expect(s.onboarding_status).toBe('pending');
    expect(s.completed_steps).toEqual(['modelserver_login']);
  });

  it('startStep POSTs to /api/step/<id> and parses oauth_url', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ state: 'started', oauth_url: 'https://x' }),
    } as Response);
    const r = await api.startStep('modelserver_login');
    expect(fetchSpy).toHaveBeenCalledWith('/api/step/modelserver_login', expect.objectContaining({ method: 'POST' }));
    expect(r.state).toBe('started');
    expect(r.oauth_url).toBe('https://x');
  });

  it('pollStepStatus returns success when state=success', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true, status: 200,
      json: async () => ({ state: 'success', key_suffix: 'wxyz' }),
    } as Response);
    const r = await api.pollStepStatus('modelserver_login');
    expect(r.state).toBe('success');
  });

  it('startVSCodeInstall returns stream_id', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true, status: 200,
      json: async () => ({ stream_id: '20260604-103301.123' }),
    } as Response);
    const r = await api.startVSCodeInstall();
    expect(r.stream_id).toBe('20260604-103301.123');
  });

  it('throws OnboardingError on non-2xx', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 500,
      text: async () => 'boom',
    } as Response);
    await expect(api.getState()).rejects.toMatchObject({
      name: 'OnboardingError',
      message: expect.stringContaining('boom'),
    });
  });

  it('throws OnboardingError on network failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'));
    await expect(api.getState()).rejects.toMatchObject({
      name: 'OnboardingError',
      message: expect.stringContaining('Failed to fetch'),
    });
  });

  it('getConsoleState returns dashboard state', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        frontend_mode: 'codex_desktop',
        frontend_name: 'Codex Desktop',
        subscription_url: 'https://code.cs.ac.cn/projects/proj-1/subscription',
        quotas: [{ window: '5h', percentage: 58, remaining_percentage: 42 }],
      }),
    } as Response);
    const s = await api.getConsoleState();
    expect(s.quotas[0].window).toBe('5h');
  });

  it('openConsoleFrontend POSTs to console endpoint', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ state: 'opened' }),
    } as Response);
    await api.openConsoleFrontend();
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/open-frontend', expect.objectContaining({ method: 'POST' }));
  });

  it('refreshConsoleState POSTs to console refresh endpoint', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        frontend_mode: 'codex_desktop',
        frontend_name: 'Codex Desktop',
        onboarding_status: 'complete',
        modelserver: {},
        agentserver: {},
        quotas: [],
      }),
    } as Response);
    await api.refreshConsoleState();
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/refresh', expect.objectContaining({ method: 'POST' }));
  });

  it('openConsoleSubscription POSTs to console subscription endpoint', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ state: 'opened' }),
    } as Response);
    await api.openConsoleSubscription();
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/open-subscription', expect.objectContaining({ method: 'POST' }));
  });

  it('logoutConsoleModelserver POSTs to console logout endpoint', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ state: 'logged_out' }),
    } as Response);
    await api.logoutConsoleModelserver();
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/logout-modelserver', expect.objectContaining({ method: 'POST' }));
  });
});
