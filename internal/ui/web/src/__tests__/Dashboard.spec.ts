import { describe, it, expect, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import Dashboard from '../components/Dashboard.vue';
import * as api from '../api';

function mockConsoleState() {
  vi.spyOn(api, 'getConsoleState').mockResolvedValue({
    frontend_mode: 'codex_desktop',
    frontend_name: 'Codex Desktop',
    onboarding_status: 'complete',
    modelserver: { project_id: 'proj-1', project_name: 'Default project' },
    agentserver: { workspace_id: 'ws-1', workspace_name: 'Default workspace' },
    subscription_url: 'https://code.cs.ac.cn/projects/proj-1/subscription',
    quotas: [
      { window: '5h', percentage: 58, remaining_percentage: 42 },
      { window: '7d', percentage: 22, remaining_percentage: 78 },
    ],
    last_refreshed_at: '2026-06-07T12:00:00Z',
  });
}

describe('Dashboard', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('renders project, workspace, quota, and subscription action', async () => {
    mockConsoleState();
    const w = mount(Dashboard);
    await flushPromises();
    expect(w.text()).toContain('Default project');
    expect(w.text()).toContain('Default workspace');
    expect(w.text()).toContain('5小时');
    expect(w.text()).toContain('剩余约 42%');
  });

  it('displays an error when opening the frontend fails', async () => {
    mockConsoleState();
    vi.spyOn(api, 'openConsoleFrontend').mockRejectedValue(new Error('open failed'));
    const w = mount(Dashboard);
    await flushPromises();

    const openButton = w.findAll('button').find(b => b.text().includes('打开 Codex Desktop'));
    expect(openButton).toBeDefined();
    await openButton!.trigger('click');
    await flushPromises();

    expect(w.text()).toContain('open failed');
  });

  it('displays an error when opening the subscription fails', async () => {
    mockConsoleState();
    vi.spyOn(api, 'openConsoleSubscription').mockRejectedValue(new Error('subscription failed'));
    const w = mount(Dashboard);
    await flushPromises();

    const subscriptionButton = w.findAll('button').find(b => b.text().includes('打开订阅页'));
    expect(subscriptionButton).toBeDefined();
    await subscriptionButton!.trigger('click');
    await flushPromises();

    expect(w.text()).toContain('subscription failed');
  });

  it('ignores duplicate refresh clicks while refresh is pending', async () => {
    mockConsoleState();
    const refreshSpy = vi.spyOn(api, 'refreshConsoleState').mockReturnValue(new Promise(() => {}));
    const w = mount(Dashboard);
    await flushPromises();

    const refreshButton = w.findAll('button').find(b => b.text().includes('刷新状态'));
    expect(refreshButton).toBeDefined();
    await refreshButton!.trigger('click');
    await refreshButton!.trigger('click');

    expect(refreshSpy).toHaveBeenCalledTimes(1);
  });

  it('ignores duplicate frontend clicks while opening is pending', async () => {
    mockConsoleState();
    const openSpy = vi.spyOn(api, 'openConsoleFrontend').mockReturnValue(new Promise(() => {}));
    const w = mount(Dashboard);
    await flushPromises();

    const openButton = w.findAll('button').find(b => b.text().includes('打开 Codex Desktop'));
    expect(openButton).toBeDefined();
    await openButton!.trigger('click');
    await openButton!.trigger('click');

    expect(openSpy).toHaveBeenCalledTimes(1);
  });

  it('ignores duplicate subscription clicks while opening is pending', async () => {
    mockConsoleState();
    const subscriptionSpy = vi.spyOn(api, 'openConsoleSubscription').mockReturnValue(new Promise(() => {}));
    const w = mount(Dashboard);
    await flushPromises();

    const subscriptionButton = w.findAll('button').find(b => b.text().includes('打开订阅页'));
    expect(subscriptionButton).toBeDefined();
    await subscriptionButton!.trigger('click');
    await subscriptionButton!.trigger('click');

    expect(subscriptionSpy).toHaveBeenCalledTimes(1);
  });
});
